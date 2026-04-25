package unixconfig

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type Provider interface {
	CurrentConfig() map[string]any
}

type Server struct {
	Path       string
	Token      string
	Provider   Provider
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

	mux := http.NewServeMux()
	mux.HandleFunc("/internal/get-config", s.handleGetConfig)
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

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.Token != "" && r.URL.Query().Get("token") != s.Token {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.Provider.CurrentConfig()); err != nil {
		slog.Warn("failed to write unix config response", "error", err)
	}
}
