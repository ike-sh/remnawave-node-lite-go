package bodylimit

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const (
	defaultMaxBytes        = 256 << 20
	lowMemoryMaxBytes      = 16 << 20
	maxCompressedBodyBytes = 64 << 20
	maxZstdWindowBytes     = 32 << 20
	maxConcurrentDecoders  = 2
	maxConfiguredMB        = 1024
)

var maxBytes atomic.Int64
var decoderSlots = make(chan struct{}, maxConcurrentDecoders)

type requestLimitKey struct{}

func init() {
	maxBytes.Store(defaultMaxBytes)
}

func Configure(lowMemory bool, bodyLimitMB int) error {
	if bodyLimitMB < 0 || bodyLimitMB > maxConfiguredMB {
		return fmt.Errorf("BODY_LIMIT_MB must be between 1 and %d MiB, or 0 for the default", maxConfiguredMB)
	}
	if lowMemory && bodyLimitMB > lowMemoryMaxBytes>>20 {
		return fmt.Errorf("BODY_LIMIT_MB must not exceed %d MiB when LOW_MEMORY=1", lowMemoryMaxBytes>>20)
	}
	if bodyLimitMB > 0 {
		maxBytes.Store(int64(bodyLimitMB) << 20)
		return nil
	}
	if lowMemory {
		maxBytes.Store(lowMemoryMaxBytes)
		return nil
	}
	maxBytes.Store(defaultMaxBytes)
	return nil
}

func MaxBytesLimit() int64 {
	return maxBytes.Load()
}

// WithRequestLimit attaches a route-specific ceiling to r. The configured
// process-wide limit remains authoritative when it is smaller.
func WithRequestLimit(r *http.Request, limit int64) *http.Request {
	if limit <= 0 {
		limit = 1
	}
	return r.WithContext(context.WithValue(r.Context(), requestLimitKey{}, limit))
}

// RequestLimit returns the effective request ceiling after applying both the
// route-specific and process-wide limits.
func RequestLimit(r *http.Request) int64 {
	limit := maxBytes.Load()
	if r == nil {
		return limit
	}
	if routeLimit, ok := r.Context().Value(requestLimitKey{}).(int64); ok && routeLimit > 0 && routeLimit < limit {
		return routeLimit
	}
	return limit
}

type decodedReadCloser struct {
	reader       io.Reader
	closeDecoder func() error
	original     io.ReadCloser
	limit        int64
	read         int64
	release      func()

	closeOnce sync.Once
	closeErr  error
}

func (d *decodedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if d.limit >= 0 {
		remaining := d.limit - d.read
		if remaining == 0 {
			var extra [1]byte
			for {
				n, err := d.reader.Read(extra[:])
				if n != 0 {
					return 0, &http.MaxBytesError{Limit: d.limit}
				}
				if err != nil {
					return 0, err
				}
			}
		}
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
	}
	n, err := d.reader.Read(p)
	d.read += int64(n)
	if errors.Is(err, zstd.ErrWindowSizeExceeded) || errors.Is(err, zstd.ErrDecoderSizeExceeded) {
		err = &http.MaxBytesError{Limit: d.limit}
	}
	return n, err
}

func (d *decodedReadCloser) Close() error {
	d.closeOnce.Do(func() {
		if d.closeDecoder != nil {
			d.closeErr = d.closeDecoder()
		}
		if d.original != nil {
			if err := d.original.Close(); d.closeErr == nil {
				d.closeErr = err
			}
		}
		if d.release != nil {
			d.release()
		}
	})
	return d.closeErr
}

func acquireDecoder(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case decoderSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-decoderSlots
			return nil, err
		}
		var once sync.Once
		return func() {
			once.Do(func() { <-decoderSlots })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newZstdDecoder(reader io.Reader, requestLimit int64) (*zstd.Decoder, error) {
	windowLimit := zstdWindowLimit(requestLimit)
	return zstd.NewReader(
		reader,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(uint64(windowLimit)),
		zstd.WithDecoderMaxWindow(uint64(windowLimit)),
	)
}

func zstdWindowLimit(requestLimit int64) int64 {
	if requestLimit <= 0 || requestLimit > maxZstdWindowBytes {
		return maxZstdWindowBytes
	}
	return requestLimit
}

func decodeBody(w http.ResponseWriter, r *http.Request, encoding string, limit int64) (*decodedReadCloser, error) {
	release, err := acquireDecoder(r.Context())
	if err != nil {
		return nil, err
	}

	compressedLimit := limit
	if compressedLimit > maxCompressedBodyBytes {
		compressedLimit = maxCompressedBodyBytes
	}
	original := http.MaxBytesReader(w, r.Body, compressedLimit)
	decoded := &decodedReadCloser{
		original: original,
		limit:    limit,
		release:  release,
	}

	err = nil
	switch encoding {
	case "gzip":
		var decoder *gzip.Reader
		decoder, err = gzip.NewReader(original)
		if err == nil {
			decoded.reader = decoder
			decoded.closeDecoder = decoder.Close
		}
	case "deflate":
		var decoder io.ReadCloser
		decoder, err = zlib.NewReader(original)
		if err == nil {
			decoded.reader = decoder
			decoded.closeDecoder = decoder.Close
		}
	case "br":
		decoded.reader = brotli.NewReader(original)
	case "zstd":
		var decoder *zstd.Decoder
		decoder, err = newZstdDecoder(original, limit)
		if err == nil {
			decoded.reader = decoder
			decoded.closeDecoder = func() error {
				decoder.Close()
				return nil
			}
		}
	default:
		err = fmt.Errorf("unsupported content encoding %q", encoding)
	}
	if encoding == "zstd" && (errors.Is(err, zstd.ErrWindowSizeExceeded) || errors.Is(err, zstd.ErrDecoderSizeExceeded)) {
		err = &http.MaxBytesError{Limit: limit}
	}
	if err != nil {
		_ = decoded.Close()
		return nil, err
	}
	return decoded, nil
}

func DecompressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}

		encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
		if encoding == "" || encoding == "identity" {
			next.ServeHTTP(w, r)
			return
		}
		switch encoding {
		case "gzip", "deflate", "br", "zstd":
		default:
			writeHTTPError(w, http.StatusUnsupportedMediaType, fmt.Sprintf("unsupported content encoding %q", encoding))
			return
		}

		decoded, err := decodeBody(w, r, encoding, RequestLimit(r))
		if errors.Is(err, context.Canceled) {
			panic(http.ErrAbortHandler)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			writeCapacityUnavailable(w, r)
			return
		}
		if err != nil {
			var limitError *http.MaxBytesError
			if errors.As(err, &limitError) {
				writeHTTPError(w, http.StatusRequestEntityTooLarge, "request entity too large")
				return
			}
			writeHTTPError(w, http.StatusBadRequest, "invalid "+encoding+" body")
			return
		}
		defer decoded.Close()
		r.Body = decoded
		r.Header.Del("Content-Encoding")
		r.ContentLength = -1
		next.ServeHTTP(w, r)
	})
}

func LimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, RequestLimit(r))
		}
		next.ServeHTTP(w, r)
	})
}

func writeCapacityUnavailable(w http.ResponseWriter, r *http.Request) {
	r.Close = true
	w.Header().Set("Connection", "close")
	w.Header().Set("Retry-After", "1")
	writeHTTPError(w, http.StatusServiceUnavailable, "request capacity unavailable")
}

func writeHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Message    string `json:"message"`
		Error      string `json:"error"`
		StatusCode int    `json:"statusCode"`
	}{
		Message:    message,
		Error:      http.StatusText(status),
		StatusCode: status,
	})
}
