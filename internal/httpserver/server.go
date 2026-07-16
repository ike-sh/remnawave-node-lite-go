package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/netutil"

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
	maxConnections int
	xrayGateOnce   sync.Once
	xrayGate       chan struct{}
	manager        xrayController
	statsService   *stats.Service
	handlerService *nodehandler.Service
	pluginService  pluginController
}

const (
	defaultMaxConnections = 128
	defaultMaxHandlers    = 32
	lowMemoryConnections  = 16
	lowMemoryHandlers     = 4
	maxRequestDuration    = 5 * time.Minute
)

type xrayController interface {
	Start(ctx context.Context, request xray.StartRequest) xray.StartResponse
	Stop() xray.StopResponse
	Health() xray.HealthResponse
}

type pluginController interface {
	ResetPluginsContext(ctx context.Context) error
	SyncContext(ctx context.Context, request *plugin.SyncPlugin) plugin.AcceptedResponse
	CollectReports() plugin.CollectReportsResponse
	BlockIPsContext(ctx context.Context, items []plugin.BlockIP) plugin.AcceptedResponse
	UnblockIPsContext(ctx context.Context, ips []string) plugin.AcceptedResponse
	RecreateTablesContext(ctx context.Context) plugin.AcceptedResponse
	ReportsCount() int
}

func New(cfg config.Config, payload secret.Payload, validator *auth.JWTValidator, manager *xray.Manager, pluginService *plugin.Service, dropper *connections.Dropper) (*Server, error) {
	tlsConfig, err := buildTLSConfig(payload)
	if err != nil {
		return nil, err
	}

	server := &Server{
		manager:        manager,
		statsService:   stats.NewService(manager, pluginService),
		handlerService: nodehandler.NewService(manager, dropper),
		pluginService:  pluginService,
	}

	maxConnections, maxHandlers := serverCapacity(cfg.LowMemory)
	nodeRoutes := bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(http.HandlerFunc(server.handleNodeRoutes)))
	limited := limitActiveHandlers(maxHandlers, withRequestTimeout(maxRequestDuration, nodeRoutes))
	protected := requireJWT(validator, requireKnownNodeRoute(limited))

	server.maxConnections = maxConnections
	server.httpServer = &http.Server{
		Addr:              cfg.HTTPAddr(),
		Handler:           rejectUnknownPaths(protected),
		ErrorLog:          newHTTPErrorLogger(),
		TLSConfig:         tlsConfig,
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	return server, nil
}

func requireJWT(validator *auth.JWTValidator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := validator.ValidateBearer(r.Header.Get("Authorization")); err != nil {
			slog.Warn("dropping request with invalid JWT", "path", r.URL.Path, "remote", r.RemoteAddr)
			panic(http.ErrAbortHandler)
		}
		next.ServeHTTP(w, r)
	})
}

func rejectUnknownPaths(nodeHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/node/") {
			panic(http.ErrAbortHandler)
		}
		nodeHandler.ServeHTTP(w, r)
	})
}

func requireKnownNodeRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := lookupNodeRoute(r.Method, r.URL.Path); !ok {
			panic(http.ErrAbortHandler)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServeTLS(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.httpServer.BaseContext = func(net.Listener) context.Context { return ctx }
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	limited := netutil.LimitListener(listener, s.maxConnections)
	err = s.httpServer.ServeTLS(limited, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func serverCapacity(lowMemory bool) (connections, handlers int) {
	if lowMemory {
		return lowMemoryConnections, lowMemoryHandlers
	}
	return defaultMaxConnections, defaultMaxHandlers
}

func limitActiveHandlers(maxActive int, next http.Handler) http.Handler {
	if maxActive <= 0 {
		maxActive = 1
	}
	slots := make(chan struct{}, maxActive)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			r.Close = true
			w.Header().Set("Connection", "close")
			w.Header().Set("Retry-After", "1")
			http.Error(w, "request capacity unavailable", http.StatusServiceUnavailable)
		}
	})
}

func withRequestTimeout(timeout time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) acquireXrayLifecycle(ctx context.Context) bool {
	s.xrayGateOnce.Do(func() { s.xrayGate = make(chan struct{}, 1) })
	select {
	case s.xrayGate <- struct{}{}:
		if ctx.Err() != nil {
			s.releaseXrayLifecycle()
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) releaseXrayLifecycle() {
	<-s.xrayGate
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Close() error {
	return s.httpServer.Close()
}

func (s *Server) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	route, ok := lookupNodeRoute(r.Method, r.URL.Path)
	if !ok {
		panic(http.ErrAbortHandler)
	}

	switch route {
	// xray
	case routeXrayHealthcheck:
		writeJSON(w, http.StatusOK, envelope[xray.HealthResponse]{Response: s.manager.Health()})
	case routeXrayStop:
		if !s.acquireXrayLifecycle(r.Context()) {
			panic(http.ErrAbortHandler)
		}
		defer s.releaseXrayLifecycle()
		response := s.manager.Stop()
		if response.IsStopped {
			if err := s.pluginService.ResetPluginsContext(r.Context()); err != nil {
				slog.Warn("failed to reset plugins after stopping Xray", "error", err)
			}
		}
		writeJSON(w, http.StatusOK, envelope[xray.StopResponse]{Response: response})
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
		s.handlePluginRecreateTables(w, r)
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
		MinVersion:   tls.VersionTLS13,
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
