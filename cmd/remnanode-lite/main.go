package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/asn"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/auth"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/bodylimit"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/config"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/doctor"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/httpserver"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/netadmin"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/secret"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/system"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/unixconfig"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/version"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xray"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Println(version.String())
			return
		case "doctor":
			os.Exit(doctor.Run(os.Args[2:]))
		case "release-url":
			if len(os.Args) < 4 {
				fmt.Fprintf(os.Stderr, "usage: remnanode-lite release-url <tag> <arch>\n")
				os.Exit(2)
			}
			fmt.Println(version.ReleaseAssetURL(os.Args[2], os.Args[3]))
			return
		case "install-script-url":
			if len(os.Args) < 4 {
				fmt.Fprintf(os.Stderr, "usage: remnanode-lite install-script-url <tag> <script>\n")
				os.Exit(2)
			}
			fmt.Println(version.InstallScriptURL(os.Args[2], os.Args[3]))
			return
		}
	}
	if err := runNode(); err != nil {
		log.Printf("remnawave-node-lite-go stopped with error: %v", err)
		os.Exit(1)
	}
}

func runNode() (runErr error) {
	cfg, err := config.Load(config.ResolveEnvPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	applyMemoryLimit(cfg.LowMemory)
	bodylimit.Configure(cfg.LowMemory, cfg.BodyLimitMB)
	if !netadmin.HasCapNetAdmin() {
		log.Printf("warning: CAP_NET_ADMIN not available — nftables plugin and ss -K connection drop are disabled (check systemd AmbientCapabilities)")
	}

	payload, err := secret.Parse(cfg.SecretKey)
	if err != nil {
		return fmt.Errorf("parse SECRET_KEY: %w", err)
	}

	validator, err := auth.NewJWTValidator(payload.JWTPublicKey)
	if err != nil {
		return fmt.Errorf("initialize JWT validator: %w", err)
	}

	manager, err := xray.NewManager(xray.Options{
		XrayBin:            cfg.XrayBin,
		GeoDir:             cfg.GeoDir,
		LogDir:             cfg.LogDir,
		InternalSocketPath: cfg.InternalSocketPath,
		InternalRESTToken:  cfg.InternalRESTToken,
		DisableHashCheck:   cfg.DisableHashedSetCheck,
		LowMemory:          cfg.LowMemory,
	})
	if err != nil {
		return fmt.Errorf("initialize Xray manager: %w", err)
	}

	pluginState := plugin.NewState()
	if asnDB, err := asn.Open(cfg.ASNDBPath); err != nil {
		log.Printf("ASN database unavailable (%s): %v — asList shared lists resolve empty", cfg.ASNDBPath, err)
	} else {
		pluginState.SetASNResolver(asnDB)
		defer func() {
			if err := asnDB.Close(); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("close ASN database: %w", err))
			}
		}()
		log.Printf("ASN database loaded from %s", cfg.ASNDBPath)
	}
	dropper := connections.NewDropper(pluginState.IsWhitelisted)
	pluginService := plugin.NewService(pluginState, dropper, manager)
	if err := pluginService.Initialize(); err != nil {
		log.Printf("warning: plugin nftables unavailable; nft-dependent plugins are disabled: %v", err)
	}
	defer func() {
		system.DefaultNetworkMonitor().Stop()
		if response := manager.Stop(); !response.IsStopped {
			runErr = errors.Join(runErr, errors.New("stop rw-core: process did not stop"))
		}
		if err := pluginService.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close plugin service: %w", err))
		}
	}()

	manager.SetTorrentBlockerProvider(pluginState)

	server, err := httpserver.New(cfg, payload, validator, manager, pluginService, dropper)
	if err != nil {
		return fmt.Errorf("initialize HTTPS server: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	unixServer := &unixconfig.Server{
		Path:     cfg.InternalSocketPath,
		Token:    cfg.InternalRESTToken,
		Provider: manager,
		Webhook:  pluginService,
	}

	serveErrors := make(chan error, 2)
	var servers sync.WaitGroup
	startServer := func(name string, serve func() error) {
		servers.Add(1)
		go func() {
			defer servers.Done()
			if err := serve(); err != nil {
				serveErrors <- fmt.Errorf("%s stopped: %w", name, err)
				return
			}
			if ctx.Err() == nil {
				serveErrors <- fmt.Errorf("%s stopped unexpectedly", name)
			}
		}()
	}

	log.Printf("internal config socket listening on %s", cfg.InternalSocketPath)
	startServer("internal config socket", func() error { return unixServer.ListenAndServe(ctx) })
	log.Printf("remnawave-node-lite-go listening on %s", cfg.HTTPAddr())
	startServer("HTTPS server", server.ListenAndServeTLS)

	go manager.StartLogRotation(ctx)

	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		runErr = errors.Join(runErr, err)
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("shutdown HTTPS server: %w", err))
		if closeErr := server.Close(); closeErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("force close HTTPS server: %w", closeErr))
		}
	}

	serversDone := make(chan struct{})
	go func() {
		servers.Wait()
		close(serversDone)
	}()
	select {
	case <-serversDone:
	case <-shutdownCtx.Done():
		runErr = errors.Join(runErr, fmt.Errorf("wait for servers: %w", shutdownCtx.Err()))
	}
	for {
		select {
		case err := <-serveErrors:
			runErr = errors.Join(runErr, err)
		default:
			return runErr
		}
	}
}

// applyMemoryLimit caps the Go runtime heap in low-memory mode (128/256MB VPS)
// regardless of init system, replacing the GOMEMLIMIT lines previously baked
// into the systemd unit / OpenRC launcher. An explicit GOMEMLIMIT env always
// wins so large nodes are never accidentally throttled.
func applyMemoryLimit(lowMemory bool) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	if lowMemory {
		debug.SetMemoryLimit(180 << 20)
		log.Printf("low-memory mode: Go soft memory limit set to 180MiB (override with GOMEMLIMIT)")
	}
}
