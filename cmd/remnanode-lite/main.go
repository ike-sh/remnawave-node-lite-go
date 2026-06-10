package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"remnawave-node-lite-go/internal/auth"
	"remnawave-node-lite-go/internal/bodylimit"
	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/doctor"
	"remnawave-node-lite-go/internal/httpserver"
	"remnawave-node-lite-go/internal/netadmin"
	"remnawave-node-lite-go/internal/plugin"
	"remnawave-node-lite-go/internal/secret"
	"remnawave-node-lite-go/internal/system"
	"remnawave-node-lite-go/internal/unixconfig"
	"remnawave-node-lite-go/internal/version"
	"remnawave-node-lite-go/internal/xray"
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
	cfg, err := config.Load(config.ResolveEnvPath())
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	bodylimit.Configure(cfg.LowMemory, cfg.BodyLimitMB)
	if !netadmin.HasCapNetAdmin() {
		log.Printf("warning: CAP_NET_ADMIN not available — nftables plugin and ss -K connection drop are disabled (check systemd AmbientCapabilities)")
	}

	payload, err := secret.Parse(cfg.SecretKey)
	if err != nil {
		log.Fatalf("parse SECRET_KEY: %v", err)
	}

	validator, err := auth.NewJWTValidator(payload.JWTPublicKey)
	if err != nil {
		log.Fatalf("initialize JWT validator: %v", err)
	}

	manager, err := xray.NewManager(xray.Options{
		XrayBin:            cfg.XrayBin,
		GeoDir:             cfg.GeoDir,
		LogDir:             cfg.LogDir,
		DataDir:            cfg.DataDir,
		InternalSocketPath: cfg.InternalSocketPath,
		InternalRESTToken:  cfg.InternalRESTToken,
		XtlsAPIPort:        cfg.XtlsAPIPort,
		DisableHashCheck:   cfg.DisableHashedSetCheck,
		LowMemory:          cfg.LowMemory,
	})
	if err != nil {
		log.Fatalf("initialize Xray manager: %v", err)
	}

	pluginState := plugin.NewState()
	dropper := connections.NewDropper(pluginState.IsWhitelisted)
	pluginService := plugin.NewService(pluginState, dropper, manager)

	manager.SetTorrentBlockerProvider(pluginState)

	server, err := httpserver.New(cfg, payload, validator, manager, pluginService, dropper)
	if err != nil {
		log.Fatalf("initialize HTTPS server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	unixServer := &unixconfig.Server{
		Path:     cfg.InternalSocketPath,
		Token:    cfg.InternalRESTToken,
		Provider: manager,
		Webhook:  pluginService,
	}
	go func() {
		log.Printf("internal config socket listening on %s", cfg.InternalSocketPath)
		if err := unixServer.ListenAndServe(ctx); err != nil {
			log.Fatalf("internal config socket stopped: %v", err)
		}
	}()

	go func() {
		log.Printf("remnawave-node-lite-go listening on %s", cfg.HTTPAddr())
		if err := server.ListenAndServeTLS(); err != nil {
			log.Fatalf("HTTPS server stopped: %v", err)
		}
	}()

	go manager.RestoreOnBoot(ctx)

	<-ctx.Done()
	system.DefaultNetworkMonitor().Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	_ = manager.Stop(false)
}
