package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultIdleTimeout  = 5 * time.Second
	defaultTotalTimeout = 15 * time.Second
	defaultMaxSize      = int64(128 * 1024 * 1024)
)

type Options struct {
	Client         *http.Client
	IdleTimeout    time.Duration
	TotalTimeout   time.Duration
	MaxSize        int64
	ExpectedSHA256 string
}

type Result struct {
	SHA256 string
	Size   int64
}

// Download fetches an HTTPS resource into a sibling temporary file and only
// replaces path after all size and digest checks pass.
func Download(ctx context.Context, rawURL, path string, opts Options) (Result, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return Result{}, fmt.Errorf("artifact URL must be an absolute HTTPS URL")
	}

	idleTimeout := opts.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	totalTimeout := opts.TotalTimeout
	if totalTimeout <= 0 {
		totalTimeout = defaultTotalTimeout
	}
	maxSize := opts.MaxSize
	if maxSize <= 0 {
		maxSize = defaultMaxSize
	}
	expected, err := normalizeSHA256(opts.ExpectedSHA256)
	if err != nil {
		return Result{}, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	idleCtx, idleCancel := context.WithCancelCause(requestCtx)
	defer idleCancel(nil)

	req, err := http.NewRequestWithContext(idleCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, fmt.Errorf("create artifact request: %w", err)
	}

	client := secureClient(opts.Client)
	response, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("download artifact: %w", err)
	}
	defer response.Body.Close()

	if response.Request == nil || response.Request.URL.Scheme != "https" {
		return Result{}, errors.New("artifact redirected to a non-HTTPS URL")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Result{}, fmt.Errorf("artifact server returned %s", response.Status)
	}
	if response.ContentLength > maxSize {
		return Result{}, fmt.Errorf("artifact content-length exceeds %d bytes", maxSize)
	}

	tmpPath := path + ".download"
	if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("remove stale artifact temporary file: %w", err)
	}
	// O_EXCL prevents a local attacker from replacing the temporary path with
	// a symlink between cleanup and creation when a custom asset directory is
	// writable by another user.
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("create artifact temporary file: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	digest := sha256.New()
	timer := time.AfterFunc(idleTimeout, func() {
		idleCancel(fmt.Errorf("no artifact data received for %s", idleTimeout))
	})
	defer timer.Stop()

	reader := &idleReader{
		reader: response.Body,
		timer:  timer,
		delay:  idleTimeout,
		ctx:    idleCtx,
	}
	size, err := copyLimited(io.MultiWriter(file, digest), reader, maxSize)
	if err != nil {
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return Result{}, cause
		}
		return Result{}, fmt.Errorf("read artifact: %w", err)
	}
	if size == 0 {
		return Result{}, errors.New("artifact response body is empty")
	}
	if err := file.Sync(); err != nil {
		return Result{}, fmt.Errorf("sync artifact temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return Result{}, fmt.Errorf("close artifact temporary file: %w", err)
	}

	actual := hex.EncodeToString(digest.Sum(nil))
	if expected != "" && actual != expected {
		return Result{}, fmt.Errorf("artifact sha256 mismatch: got %s, expected %s", actual, expected)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return Result{}, fmt.Errorf("activate artifact: %w", err)
	}
	committed = true

	return Result{SHA256: actual, Size: size}, nil
}

func secureClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	clone := *base
	previous := base.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("artifact redirect target must use HTTPS")
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &clone
}

func normalizeSHA256(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("expected SHA-256 must contain exactly 64 hexadecimal characters")
	}
	return value, nil
}

func copyLimited(dst io.Writer, src io.Reader, maxSize int64) (int64, error) {
	written, err := io.Copy(dst, io.LimitReader(src, maxSize+1))
	if err != nil {
		return written, err
	}
	if written > maxSize {
		return written, fmt.Errorf("artifact body exceeds %d bytes", maxSize)
	}
	return written, nil
}

type idleReader struct {
	reader io.Reader
	timer  *time.Timer
	delay  time.Duration
	ctx    context.Context
}

func (r *idleReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, context.Cause(r.ctx)
	default:
	}
	n, err := r.reader.Read(p)
	if n > 0 {
		r.timer.Reset(r.delay)
	}
	return n, err
}
