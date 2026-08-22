package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultGeocheckBin = "/usr/local/bin/geocheck"
	geocheckTimeout    = 45 * time.Second
	geocheckMaxOutput  = 32 << 20
	geocheckMaxStderr  = 64 << 10
)

var (
	errGeocheckOutputTooLarge = errors.New("geocheck output exceeds 32 MiB")
	errGeocheckStderrTooLarge = errors.New("geocheck stderr exceeds 64 KiB")
)

type geocheckRunner func(ctx context.Context, bin string, args ...string) ([]byte, error)

type geocheckService struct {
	bin       string
	running   atomic.Bool
	run       geocheckRunner
	timeout   time.Duration
	maxOutput int
}

func newGeocheckService(bin string) *geocheckService {
	return &geocheckService{
		bin:       bin,
		run:       runGeocheckCommand,
		timeout:   geocheckTimeout,
		maxOutput: geocheckMaxOutput,
	}
}

type geocheckRequest struct {
	IP        *string `json:"ip"`
	Interface *string `json:"interface"`
}

func (s *Service) HandleGetGeocheck(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	defer r.Body.Close()
	var body geocheckRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		write(w, http.StatusBadRequest, map[string]any{"message": "invalid JSON body"})
		return
	}

	if s.geocheck == nil {
		writeAPIErrorMessage(write, w, errFailedGeocheck, "Geocheck: service is not configured.")
		return
	}
	report, message := s.geocheck.execute(r.Context(), body)
	if message != "" {
		writeAPIErrorMessage(write, w, errFailedGeocheck, message)
		return
	}
	write(w, http.StatusOK, envelope[map[string]any]{Response: report})
}

func (s *geocheckService) execute(parent context.Context, body geocheckRequest) (map[string]any, string) {
	ip := optionalTrimmed(body.IP)
	iface := optionalTrimmed(body.Interface)
	bindTo := ""
	if ip != "" {
		if net.ParseIP(ip) == nil {
			return nil, fmt.Sprintf("Geocheck: %q is not a valid IP address.", ip)
		}
		bindTo = ip
	} else if iface != "" {
		bindTo = iface
	}

	if !s.running.CompareAndSwap(false, true) {
		return nil, "Geocheck: a run is already in progress."
	}
	defer s.running.Store(false)

	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	args := make([]string, 0, 6)
	if bindTo != "" {
		args = append(args, "--interface", bindTo)
	}
	args = append(args, "--json", "--svg-base64", "--quiet")

	output, err := s.run(ctx, s.bin, args...)
	target := bindTo
	if target == "" {
		target = "default route"
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Sprintf("Geocheck via %s exceeded %dms and was killed.", target, s.timeout.Milliseconds())
		}
		return nil, fmt.Sprintf("Geocheck via %s failed: %v", target, err)
	}
	if len(output) > s.maxOutput {
		return nil, fmt.Sprintf("Geocheck via %s failed: %v", target, errGeocheckOutputTooLarge)
	}

	var report map[string]any
	if err := json.Unmarshal(output, &report); err != nil {
		return nil, fmt.Sprintf("Geocheck via %s failed: invalid JSON: %v", target, err)
	}
	image, ok := report["image"].(map[string]any)
	if !ok || image["format"] != "svg" || image["media_type"] != "image/svg+xml" || image["encoding"] != "base64" {
		return nil, fmt.Sprintf("Geocheck via %s failed: invalid image metadata", target)
	}
	data, _ := image["data"].(string)
	if data == "" {
		return nil, fmt.Sprintf("Geocheck via %s failed: geocheck report carries no image", target)
	}
	return report, ""
}

func optionalTrimmed(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func runGeocheckCommand(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout := &limitedBuffer{remaining: geocheckMaxOutput}
	stderr := &limitedBuffer{remaining: geocheckMaxStderr, overflow: errGeocheckStderrTooLarge}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(stdout.err, errGeocheckOutputTooLarge) {
			return nil, errGeocheckOutputTooLarge
		}
		if errors.Is(stderr.err, errGeocheckStderrTooLarge) {
			return nil, errGeocheckStderrTooLarge
		}
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	if stdout.err != nil {
		return nil, stdout.err
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	overflow  error
	err       error
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if len(p) > b.remaining {
		written := b.remaining
		if b.remaining > 0 {
			_, _ = b.buffer.Write(p[:b.remaining])
		}
		b.remaining = 0
		b.err = b.overflow
		if b.err == nil {
			b.err = errGeocheckOutputTooLarge
		}
		return written, b.err
	}
	n, err := b.buffer.Write(p)
	b.remaining -= n
	return n, err
}

func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedBuffer) String() string { return b.buffer.String() }

var _ io.Writer = (*limitedBuffer)(nil)
