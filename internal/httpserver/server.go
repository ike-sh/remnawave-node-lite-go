package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"remnawave-node-lite-go/internal/auth"
	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/secret"
	"remnawave-node-lite-go/internal/xray"
)

type Server struct {
	httpServer *http.Server
	manager    *xray.Manager
}

func New(cfg config.Config, payload secret.Payload, validator *auth.JWTValidator, manager *xray.Manager) (*Server, error) {
	tlsConfig, err := buildTLSConfig(payload)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	server := &Server{manager: manager}

	protected := validator.Middleware(http.HandlerFunc(server.handleNodeRoutes))
	mux.Handle("/node/", protected)

	server.httpServer = &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           mux,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return server, nil
}

func (s *Server) ListenAndServeTLS() error {
	err := s.httpServer.ListenAndServeTLS("", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/node/xray/healthcheck":
		writeJSON(w, http.StatusOK, envelope[xray.HealthResponse]{Response: s.manager.Health()})
	case r.Method == http.MethodGet && r.URL.Path == "/node/xray/stop":
		writeJSON(w, http.StatusOK, envelope[xray.StopResponse]{Response: s.manager.Stop()})
	case r.Method == http.MethodPost && r.URL.Path == "/node/xray/start":
		s.handleStart(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var request xray.StartRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if request.XrayConfig == nil {
		writeError(w, http.StatusBadRequest, "xrayConfig is required")
		return
	}

	writeJSON(w, http.StatusOK, envelope[xray.StartResponse]{Response: s.manager.Start(r.Context(), request)})
}

func buildTLSConfig(payload secret.Payload) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair([]byte(payload.NodeCertPEM), []byte(payload.NodeKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("load node TLS certificate: %w", err)
	}

	clientCAs := x509.NewCertPool()
	if ok := clientCAs.AppendCertsFromPEM([]byte(payload.CACertPEM)); !ok {
		return nil, errors.New("append client CA certificate: no certificates found")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

type envelope[T any] struct {
	Response T `json:"response"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("failed to write JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"message":   message,
	})
}
