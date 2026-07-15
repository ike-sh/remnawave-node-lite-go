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

	"github.com/Luxiaba/remnawave-node-lite-go/internal/auth"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/bodylimit"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/config"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodehandler"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/secret"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/stats"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xray"
)

type Server struct {
	httpServer     *http.Server
	manager        xrayController
	statsService   *stats.Service
	handlerService *nodehandler.Service
	pluginService  pluginController
}

type xrayController interface {
	Start(ctx context.Context, request xray.StartRequest) xray.StartResponse
	Stop() xray.StopResponse
	Health() xray.HealthResponse
}

type pluginController interface {
	ResetPlugins()
	Sync(request *plugin.SyncPlugin) plugin.AcceptedResponse
	CollectReports() plugin.CollectReportsResponse
	BlockIPs(items []plugin.BlockIP) plugin.AcceptedResponse
	UnblockIPs(ips []string) plugin.AcceptedResponse
	RecreateTables() plugin.AcceptedResponse
	ReportsCount() int
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
	}

	protected := validator.Middleware(bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(http.HandlerFunc(server.handleNodeRoutes))))
	mux.Handle("/node/", protected)

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

func (s *Server) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	route, ok := lookupNodeRoute(r.Method, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch route {
	// xray
	case routeXrayHealthcheck:
		writeJSON(w, http.StatusOK, envelope[xray.HealthResponse]{Response: s.manager.Health()})
	case routeXrayStop:
		s.pluginService.ResetPlugins()
		writeJSON(w, http.StatusOK, envelope[xray.StopResponse]{Response: s.manager.Stop()})
	case routeXrayStart:
		s.handleStart(w, r)

	// stats
	case routeStatsGetUserOnlineStatus:
		s.handleStatsGetUserOnlineStatus(w, r)
	case routeStatsGetSystemStats:
		s.handleStatsGetSystemStats(w, r)
	case routeStatsGetUsersStats:
		s.handleStatsGetUsersStats(w, r)
	case routeStatsGetInboundStats:
		s.handleStatsGetInboundStats(w, r)
	case routeStatsGetOutboundStats:
		s.handleStatsGetOutboundStats(w, r)
	case routeStatsGetAllInboundsStats:
		s.handleStatsGetAllInboundsStats(w, r)
	case routeStatsGetAllOutboundsStats:
		s.handleStatsGetAllOutboundsStats(w, r)
	case routeStatsGetCombinedStats:
		s.handleStatsGetCombinedStats(w, r)
	case routeStatsGetUserIPList:
		s.handleStatsGetUserIPList(w, r)
	case routeStatsGetUsersIPList:
		s.handleStatsGetUsersIPList(w, r)

	// handler
	case routeHandlerAddUser:
		s.handleAddUser(w, r)
	case routeHandlerRemoveUser:
		s.handleRemoveUser(w, r)
	case routeHandlerGetInboundUsersCount:
		s.handleGetInboundUsersCount(w, r)
	case routeHandlerGetInboundUsers:
		s.handleGetInboundUsers(w, r)
	case routeHandlerAddUsers:
		s.handleAddUsers(w, r)
	case routeHandlerRemoveUsers:
		s.handleRemoveUsers(w, r)
	case routeHandlerDropUsersConnections:
		s.handleDropUsersConnections(w, r)
	case routeHandlerDropIPs:
		s.handleDropIPs(w, r)

	// plugin
	case routePluginSync:
		s.handlePluginSync(w, r)
	case routePluginCollectTorrentReports:
		s.handlePluginCollectReports(w)
	case routePluginBlockIPs:
		s.handlePluginBlockIPs(w, r)
	case routePluginUnblockIPs:
		s.handlePluginUnblockIPs(w, r)
	case routePluginRecreateTables:
		s.handlePluginRecreateTables(w)
	}
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
