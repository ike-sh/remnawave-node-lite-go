package httpserver

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"remnawave-node-lite-go/internal/auth"
	"remnawave-node-lite-go/internal/bodylimit"
	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/nodehandler"
	"remnawave-node-lite-go/internal/plugin"
	"remnawave-node-lite-go/internal/secret"
	"remnawave-node-lite-go/internal/stats"
	"remnawave-node-lite-go/internal/xray"
)

type Server struct {
	httpServer     *http.Server
	manager        *xray.Manager
	statsService   *stats.Service
	handlerService *nodehandler.Service
	pluginService  *plugin.Service
}

type requestValidatedKey struct{}

func New(cfg config.Config, payload secret.Payload, validator *auth.JWTValidator, manager *xray.Manager, pluginService *plugin.Service, dropper *connections.Dropper) (*Server, error) {
	tlsConfig, err := buildTLSConfig(payload, cfg.SNIVerification)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	server := &Server{
		manager:        manager,
		statsService:   stats.NewService(manager, pluginService, cfg.GeocheckBin),
		handlerService: nodehandler.NewService(manager, dropper),
		pluginService:  pluginService,
	}

	jwtProtected := validator.Middleware(http.HandlerFunc(server.handleNodeRoutes))
	parsed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validatePostRequest(w, r) {
			return
		}
		jwtProtected.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestValidatedKey{}, true)))
	})
	protected := bodylimit.DecompressMiddleware(bodylimit.LimitMiddleware(parsed))
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
	if validated, _ := r.Context().Value(requestValidatedKey{}).(bool); !validated {
		if !validatePostRequest(w, r) {
			return
		}
	}
	path := r.URL.Path
	write := func(w http.ResponseWriter, status int, value any) {
		if r.Method == http.MethodPost && status == http.StatusOK {
			status = http.StatusCreated
		}
		if body, ok := value.(map[string]any); ok {
			if _, coded := body["errorCode"]; coded {
				body["path"] = r.URL.RequestURI()
			}
		}
		writeJSON(w, status, value)
	}

	switch {
	// xray
	case r.Method == http.MethodGet && path == "/node/xray/healthcheck":
		writeJSON(w, http.StatusOK, envelope[xray.HealthResponse]{Response: s.manager.Health()})
	case r.Method == http.MethodGet && path == "/node/xray/stop":
		s.pluginService.ResetPlugins()
		writeJSON(w, http.StatusOK, envelope[xray.StopResponse]{Response: s.manager.Stop(true)})
	case r.Method == http.MethodPost && path == "/node/xray/start":
		s.handleStart(w, r)

	// stats
	case r.Method == http.MethodPost && path == "/node/stats/get-user-online-status":
		s.statsService.HandleGetUserOnlineStatus(w, r, write)
	case r.Method == http.MethodPost && path == "/node/stats/get-geocheck":
		s.statsService.HandleGetGeocheck(w, r, write)
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
		// Official NotFoundExceptionFilter destroys the socket for unknown routes.
		panic(http.ErrAbortHandler)
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

	writeJSON(w, http.StatusCreated, envelope[xray.StartResponse]{Response: s.manager.Start(r.Context(), request)})
}

func buildTLSConfig(payload secret.Payload, sniVerification bool) (*tls.Config, error) {
	certificate, err := tls.X509KeyPair([]byte(payload.NodeCertPEM), []byte(payload.NodeKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("load node TLS certificate: %w", err)
	}

	clientCAs := x509.NewCertPool()
	if ok := clientCAs.AppendCertsFromPEM([]byte(payload.CACertPEM)); !ok {
		return nil, errors.New("append client CA certificate: no certificates found")
	}
	config := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	if !sniVerification {
		return config, nil
	}
	expectedSNI, err := secret.DeriveSNI(payload.CACertPEM, payload.JWTPublicKey)
	if err != nil {
		return nil, err
	}
	expected := []byte(expectedSNI)
	config.GetConfigForClient = func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		got := []byte(hello.ServerName)
		if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
			return nil, errors.New("unknown sni")
		}
		// A nil config keeps the active net/http TLS config, including ALPN and
		// HTTP/2 settings that ListenAndServeTLS may add to its runtime clone.
		return nil, nil
	}
	return config, nil
}

type envelope[T any] struct {
	Response T `json:"response"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("failed to write JSON response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message":   message,
	})
}
