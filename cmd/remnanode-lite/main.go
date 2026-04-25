package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"remnawave-node-lite-go/internal/auth"
	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/httpserver"
	"remnawave-node-lite-go/internal/secret"
	"remnawave-node-lite-go/internal/unixconfig"
	"remnawave-node-lite-go/internal/xray"
)

func main() {
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("load config: %v", err)
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
		InternalSocketPath: cfg.InternalSocketPath,
		InternalRESTToken:  cfg.InternalRESTToken,
		XtlsAPIPort:        cfg.XtlsAPIPort,
	})
	if err != nil {
		log.Fatalf("initialize Xray manager: %v", err)
	}
	server, err := httpserver.New(cfg, payload, validator, manager)
	if err != nil {
		log.Fatalf("initialize HTTPS server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	unixServer := &unixconfig.Server{
		Path:     cfg.InternalSocketPath,
		Token:    cfg.InternalRESTToken,
		Provider: manager,
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

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	_ = manager.Stop()
}
