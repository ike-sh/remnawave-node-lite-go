package unixconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// InternalTokenHeader is the preferred auth channel (not visible in process argv).
const InternalTokenHeader = "X-Internal-Token"

// InternalTokenEnvVar is passed to rw-core for future header-based auth.
const InternalTokenEnvVar = "RNL_INTERNAL_REST_TOKEN"

type Provider interface {
	// CurrentConfigJSON returns the pre-serialized config; the server writes
	// it verbatim so large configs are not re-marshaled on every core poll.
	CurrentConfigJSON() []byte
}

type WebhookProcessor interface {
	HandleXrayWebhook(payload map[string]any)
}

type Server struct {
	Path       string
	Token      string
	Provider   Provider
	Webhook    WebhookProcessor
	httpServer *http.Server
}

func (s *Server) ListenAndServe(ctx context.Context) error {
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

	_ = os.Remove(s.Path)
	listener, err := net.Listen("unix", s.Path)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.Path, 0o600); err != nil {
		_ = listener.Close()
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/internal/get-config", s.handleGetConfig)
	mux.HandleFunc("/internal/webhook", s.handleWebhook)
	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Warn("failed to shutdown unix config server", "error", err)
		}
		_ = os.Remove(s.Path)
	}()

	err = s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
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

	if s.Webhook != nil {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			slog.Warn("invalid xray webhook JSON", "error", err)
		} else {
			s.Webhook.HandleXrayWebhook(payload)
		}
	} else {
		_, _ = io.Copy(io.Discard, r.Body)
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
