package unixconfig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xraywebhook"
	"golang.org/x/net/netutil"
	"golang.org/x/sys/unix"
)

// InternalTokenHeader is the preferred auth channel (not visible in process argv).
const InternalTokenHeader = "X-Internal-Token"

// InternalTokenEnvVar is passed to rw-core for future header-based auth.
const InternalTokenEnvVar = "RNL_INTERNAL_REST_TOKEN"

const (
	maxWebhookBodyBytes       = 8 << 10
	maxUnixConnections        = 8
	maxConcurrentUnixHandlers = 4
	maxUnixHeaderBytes        = 8 << 10
	unixSocketProbeTimeout    = 250 * time.Millisecond
)

type Provider interface {
	// CurrentConfigJSON returns the pre-serialized config; the server writes
	// it verbatim so large configs are not re-marshaled on every core poll.
	CurrentConfigJSON() []byte
}

type WebhookProcessor interface {
	HandleXrayWebhookContext(ctx context.Context, payload xraywebhook.Payload) bool
}

type Server struct {
	Path       string
	Token      string
	Provider   Provider
	Webhook    WebhookProcessor
	httpServer *http.Server
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancelServe := context.WithCancel(ctx)
	defer cancelServe()
	if s.Path == "" {
		return errors.New("unix socket path is required")
	}
	if s.Provider == nil {
		return errors.New("config provider is required")
	}

	if dir := filepath.Dir(s.Path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	dirLock, err := lockUnixSocketDirectory(s.Path)
	if err != nil {
		return err
	}
	defer func() {
		if err := dirLock.Close(); err != nil {
			slog.Warn("failed to release unix config socket directory lock", "path", s.Path, "error", err)
		}
	}()

	if err := prepareUnixSocketPath(s.Path); err != nil {
		return err
	}
	unixListener, socketInfo, err := listenUnixSocket(s.Path)
	if err != nil {
		return err
	}
	defer func() {
		if err := removeOwnedUnixSocket(s.Path, socketInfo); err != nil {
			slog.Warn("failed to clean up unix config socket", "path", s.Path, "error", err)
		}
	}()
	listener := net.Listener(unixListener)
	listener = netutil.LimitListener(listener, maxUnixConnections)

	mux := http.NewServeMux()
	mux.HandleFunc("/internal/get-config", s.handleGetConfig)
	mux.HandleFunc("/internal/webhook", s.handleWebhook)
	s.httpServer = &http.Server{
		Handler:           withUnixRequestTimeout(30*time.Second, limitUnixHandlers(maxConcurrentUnixHandlers, mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    maxUnixHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Warn("failed to shutdown unix config server", "error", err)
			if closeErr := s.httpServer.Close(); closeErr != nil {
				slog.Warn("failed to force-close unix config server", "error", closeErr)
			}
		}
	}()

	err = s.httpServer.Serve(listener)
	cancelServe()
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type unixSocketDirectoryLock struct {
	fd   int
	path string
}

func lockUnixSocketDirectory(socketPath string) (*unixSocketDirectoryLock, error) {
	dir := filepath.Dir(socketPath)
	if dir == "" {
		dir = "."
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open unix socket directory %q for locking: %w", dir, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("lock unix socket directory %q: %w", dir, err)
	}
	return &unixSocketDirectoryLock{fd: fd, path: dir}, nil
}

func (l *unixSocketDirectoryLock) Close() error {
	if l == nil || l.fd < 0 {
		return nil
	}
	fd := l.fd
	l.fd = -1
	unlockErr := unix.Flock(fd, unix.LOCK_UN)
	closeErr := unix.Close(fd)
	if unlockErr != nil {
		unlockErr = fmt.Errorf("unlock unix socket directory %q: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close unix socket directory %q: %w", l.path, closeErr)
	}
	return errors.Join(unlockErr, closeErr)
}

func prepareUnixSocketPath(path string) error {
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing unix socket %q: %w", path, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink at unix socket path %q", path)
	}
	if before.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket at unix socket path %q", path)
	}

	conn, dialErr := net.DialTimeout("unix", path, unixSocketProbeTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("unix socket %q is already accepting connections", path)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("probe existing unix socket %q: %w", path, dialErr)
	}

	after, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect stale unix socket %q: %w", path, err)
	}
	if !sameUnixSocket(before, after) {
		return fmt.Errorf("unix socket %q changed while checking whether it was stale", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale unix socket %q: %w", path, err)
	}
	return nil
}

func listenUnixSocket(path string) (*net.UnixListener, os.FileInfo, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, nil, fmt.Errorf("listen on unix socket %q: %w", path, err)
	}
	// Go otherwise unlinks Path from UnixListener.Close without checking that
	// the directory entry still belongs to this listener.
	listener.SetUnlinkOnClose(false)

	socketInfo, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		return nil, nil, fmt.Errorf("inspect newly bound unix socket %q: %w", path, err)
	}
	if socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode()&os.ModeSymlink != 0 {
		_ = listener.Close()
		return nil, nil, fmt.Errorf("newly bound unix socket path %q was replaced", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = removeOwnedUnixSocket(path, socketInfo)
		return nil, nil, fmt.Errorf("set unix socket permissions %q: %w", path, err)
	}
	current, err := os.Lstat(path)
	if err != nil || !sameUnixSocket(socketInfo, current) {
		_ = listener.Close()
		_ = removeOwnedUnixSocket(path, socketInfo)
		if err != nil {
			return nil, nil, fmt.Errorf("reinspect newly bound unix socket %q: %w", path, err)
		}
		return nil, nil, fmt.Errorf("newly bound unix socket path %q changed while setting permissions", path)
	}
	return listener, socketInfo, nil
}

func removeOwnedUnixSocket(path string, owned os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sameUnixSocket(owned, current) {
		return nil
	}
	return os.Remove(path)
}

func sameUnixSocket(first, second os.FileInfo) bool {
	return first != nil && second != nil &&
		first.Mode()&os.ModeSocket != 0 && second.Mode()&os.ModeSocket != 0 &&
		first.Mode()&os.ModeSymlink == 0 && second.Mode()&os.ModeSymlink == 0 &&
		os.SameFile(first, second)
}

func limitUnixHandlers(maxActive int, next http.Handler) http.Handler {
	if maxActive <= 0 {
		maxActive = 1
	}
	totalSlots := make(chan struct{}, maxActive)
	webhookCapacity := maxActive
	if webhookCapacity > 1 {
		webhookCapacity--
	}
	webhookSlots := make(chan struct{}, webhookCapacity)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/webhook" {
			if !acquireUnixHandlerSlot(r.Context(), webhookSlots) {
				writeUnixCapacityError(w, r)
				return
			}
			defer func() { <-webhookSlots }()
		}
		if !acquireUnixHandlerSlot(r.Context(), totalSlots) {
			writeUnixCapacityError(w, r)
			return
		}
		defer func() { <-totalSlots }()
		next.ServeHTTP(w, r)
	})
}

func acquireUnixHandlerSlot(ctx context.Context, slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		if ctx.Err() != nil {
			<-slots
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func writeUnixCapacityError(w http.ResponseWriter, r *http.Request) {
	r.Close = true
	w.Header().Set("Connection", "close")
	w.Header().Set("Retry-After", "1")
	http.Error(w, "internal request capacity unavailable", http.StatusServiceUnavailable)
}

func withUnixRequestTimeout(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeInternal(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	limitedBody := http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes)
	defer limitedBody.Close()

	if s.Webhook != nil {
		payload, err := xraywebhook.Decode(limitedBody)
		if err != nil {
			slog.Warn("invalid xray webhook JSON", "error", err)
		} else {
			if !s.Webhook.HandleXrayWebhookContext(r.Context(), payload) {
				r.Close = true
				w.Header().Set("Connection", "close")
				w.Header().Set("Retry-After", "1")
				http.Error(w, "webhook capacity unavailable", http.StatusServiceUnavailable)
				return
			}
		}
	} else {
		_, _ = io.Copy(io.Discard, limitedBody)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeInternal(r) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(s.Provider.CurrentConfigJSON()); err != nil {
		slog.Warn("failed to write unix config response", "error", err)
	}
}

// authorizeInternal accepts X-Internal-Token, deprecated ?token=, or owner-only unix socket (0600).
func (s *Server) authorizeInternal(r *http.Request) bool {
	if s.Token == "" {
		slog.Warn("internal REST token not configured; rejecting request")
		return false
	}
	header := r.Header.Get(InternalTokenHeader)
	query := r.URL.Query().Get("token")
	if header != "" || query != "" {
		return header == s.Token || query == s.Token
	}
	return true
}
