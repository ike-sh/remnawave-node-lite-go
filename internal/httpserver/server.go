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
	"strings"
	"time"

	"remnawave-node-lite-go/internal/auth"
	"remnawave-node-lite-go/internal/bodylimit"
	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/nodehandler"
	"remnawave-node-lite-go/internal/plugin"
	"remnawave-node-lite-go/internal/secret"
	"remnawave-node-lite-go/internal/stats"
	"remnawave-node-lite-go/internal/vision"
	"remnawave-node-lite-go/internal/xray"
)

type Server struct {
	httpServer     *http.Server
	manager        *xray.Manager
	statsService   *stats.Service
	handlerService *nodehandler.Service
	pluginService  *plugin.Service
	visionService  *vision.Service
}

func New(cfg config.Config, payload secret.Payload, validator *auth.JWTValidator, manager *xray.Manager, pluginService *plugin.Service, dropper *connections.Dropper) (*Server, error) {
	tlsConfig, err := buildTLSConfig(payload)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	server := &Server{
		manager:        manager,
		statsService:   stats.NewService(manager, pluginService),
		handlerService: nodehandler.NewService(manager, dropper),
		pluginService:  pluginService,
		visionService:  vision.NewService(manager),
	}

	protected := validator.Middleware(bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(http.HandlerFunc(server.handleProtectedRoutes))))
	mux.Handle("/node/", protected)
	mux.Handle("/vision/", protected)

	server.httpServer = &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           mux,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
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

func (s *Server) handleProtectedRoutes(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/vision/") {
		s.handleVisionRoutes(w, r)
		return
	}
	s.handleNodeRoutes(w, r)
}

func (s *Server) handleVisionRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	write := writeJSON
	switch {
	case r.Method == http.MethodPost && path == "/vision/block-ip":
		s.visionService.HandleBlockIP(w, r, write)
	case r.Method == http.MethodPost && path == "/vision/unblock-ip":
		s.visionService.HandleUnblockIP(w, r, write)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	write := writeJSON

	switch {
	// xray
	case r.Method == http.MethodGet && path == "/node/xray/healthcheck":
		writeJSON(w, http.StatusOK, envelope[xray.HealthResponse]{Response: s.manager.Health()})
	case (r.Method == http.MethodPost || r.Method == http.MethodGet) && path == "/node/xray/stop":
		if r.Method == http.MethodGet {
			slog.Warn("deprecated HTTP method for /node/xray/stop; use POST")
		}
		s.pluginService.ResetPlugins()
		writeJSON(w, http.StatusOK, envelope[xray.StopResponse]{Response: s.manager.Stop(true)})
	case r.Method == http.MethodPost && path == "/node/xray/start":
		s.handleStart(w, r)

	// stats
	case r.Method == http.MethodPost && path == "/node/stats/get-user-online-status":
		s.statsService.HandleGetUserOnlineStatus(w, r, write)
	case r.Method == http.MethodGet && path == "/node/stats/get-system-stats":
		s.statsService.HandleGetSystemStats(w, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-users-stats":
		s.statsService.HandleGetUsersStats(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-inbound-stats":
		s.statsService.HandleGetInboundStats(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-outbound-stats":
		s.statsService.HandleGetOutboundStats(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-all-inbounds-stats":
		s.statsService.HandleGetAllInboundsStats(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-all-outbounds-stats":
		s.statsService.HandleGetAllOutboundsStats(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-combined-stats":
		s.statsService.HandleGetCombinedStats(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-user-ip-list":
		s.statsService.HandleGetUserIPList(w, r, write)
	case r.Method == http.MethodGet && path == "/node/stats/get-users-ip-list":
		s.statsService.HandleGetUsersIPList(w, r, write)

	// handler
	case r.Method == http.MethodPost && path == "/node/handler/add-user":
		s.handlerService.HandleAddUser(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/remove-user":
		s.handlerService.HandleRemoveUser(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/get-inbound-users-count":
		s.handlerService.HandleGetInboundUsersCount(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/get-inbound-users":
		s.handlerService.HandleGetInboundUsers(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/add-users":
		s.handlerService.HandleAddUsers(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/remove-users":
		s.handlerService.HandleRemoveUsers(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/drop-users-connections":
		s.handlerService.HandleDropUsersConnections(w, r, write)
	case r.Method == http.MethodPost && path == "/node/handler/drop-ips":
		s.handlerService.HandleDropIPs(w, r, write)

	// plugin
	case r.Method == http.MethodPost && path == "/node/plugin/sync":
		s.pluginService.HandleSync(w, r, write)
	case r.Method == http.MethodPost && path == "/node/plugin/torrent-blocker/collect":
		s.pluginService.HandleCollectReports(w, write)
	case r.Method == http.MethodPost && path == "/node/plugin/nftables/block-ips":
		s.pluginService.HandleBlockIPs(w, r, write)
	case r.Method == http.MethodPost && path == "/node/plugin/nftables/unblock-ips":
		s.pluginService.HandleUnblockIPs(w, r, write)
	case r.Method == http.MethodPost && path == "/node/plugin/nftables/recreate-tables":
		s.pluginService.HandleRecreateTables(w, r, write)

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
