package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
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

const nodeShutdownTimeout = 25 * time.Second

const cliUsage = "usage: remnanode-lite [version|doctor|validate-secret|canonicalize-secret|release-url|install-script-url]"

func main() {
	code := runCLI(
		os.Args[1:],
		os.Stdin,
		os.Stdout,
		os.Stderr,
		runNode,
		doctor.Run,
	)
	if code != 0 {
		os.Exit(code)
	}
}

func runCLI(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	runDaemon func() error,
	runDoctor func([]string) int,
) int {
	if len(args) == 0 {
		if err := runDaemon(); err != nil {
			fmt.Fprintf(stderr, "remnawave-node-lite-go stopped with error: %v\n", err)
			return 1
		}
		return 0
	}

	usageError := func(usage string) int {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	writeLine := func(value string) int {
		if _, err := fmt.Fprintln(stdout, value); err != nil {
			fmt.Fprintf(stderr, "write command output: %v\n", err)
			return 1
		}
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError(cliUsage)
		}
		return writeLine(cliUsage)
	case "version", "-version", "--version":
		if len(args) != 1 {
			return usageError("usage: remnanode-lite version")
		}
		return writeLine(version.String())
	case "doctor":
		doctorArgs := args[1:]
		if len(doctorArgs) != 0 && (len(doctorArgs) != 2 || doctorArgs[0] != "--env" || doctorArgs[1] == "") {
			return usageError("usage: remnanode-lite doctor [--env PATH]")
		}
		return runDoctor(doctorArgs)
	case "validate-secret":
		if len(args) != 1 {
			return usageError("usage: remnanode-lite validate-secret < SECRET_KEY")
		}
		return validateSecretCommand(stdin, stderr)
	case "canonicalize-secret":
		if len(args) != 2 {
			return usageError("usage: remnanode-lite canonicalize-secret <path|->")
		}
		return canonicalizeSecretCommand(args[1], stdin, stdout, stderr)
	case "release-url":
		if len(args) != 3 {
			return usageError("usage: remnanode-lite release-url <tag> <arch>")
		}
		return writeLine(version.ReleaseAssetURL(args[1], args[2]))
	case "install-script-url":
		if len(args) != 3 {
			return usageError("usage: remnanode-lite install-script-url <tag> <script>")
		}
		return writeLine(version.InstallScriptURL(args[1], args[2]))
	default:
		fmt.Fprintf(stderr, "Unknown command: %s\n", args[0])
		fmt.Fprintln(stderr, cliUsage)
		return 1
	}
}

func validateSecretCommand(input io.Reader, stderr io.Writer) int {
	canonical, err := canonicalSecretFromReader(input)
	if err != nil {
		fmt.Fprintf(stderr, "read SECRET_KEY: %v\n", err)
		return 1
	}
	if _, err := secret.Parse(canonical); err != nil {
		fmt.Fprintf(stderr, "invalid SECRET_KEY: %v\n", err)
		return 1
	}
	return 0
}

func canonicalizeSecretCommand(path string, stdin io.Reader, stdout, stderr io.Writer) int {
	var (
		canonical string
		err       error
	)
	if path == "-" {
		canonical, err = canonicalSecretFromReader(stdin)
	} else {
		canonical, err = config.ReadSecretFile(path)
	}
	if err != nil {
		fmt.Fprintf(stderr, "read Secret Key source: %v\n", err)
		return 1
	}
	if _, err := secret.Parse(canonical); err != nil {
		fmt.Fprintf(stderr, "invalid SECRET_KEY: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, canonical); err != nil {
		fmt.Fprintf(stderr, "write canonical SECRET_KEY: %v\n", err)
		return 1
	}
	return 0
}

func canonicalSecretFromReader(input io.Reader) (string, error) {
	maxInputBytes := int64(secret.MaxEncodedBytes + 2)
	raw, err := io.ReadAll(io.LimitReader(input, maxInputBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > maxInputBytes {
		return "", fmt.Errorf("SECRET_KEY input exceeds %d bytes", maxInputBytes)
	}
	return config.CanonicalizeSecretFileContent(raw)
}

func runNode() (runErr error) {
	cfg, err := config.Load(runtimeEnvPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := bodylimit.Configure(cfg.LowMemory, cfg.BodyLimitMB); err != nil {
		return fmt.Errorf("configure request body limit: %w", err)
	}
	if err := applyRuntimeOverrides(cfg); err != nil {
		return err
	}
	applyMemoryLimit(cfg)
	if !netadmin.HasCapNetAdmin() {
		log.Printf("warning: CAP_NET_ADMIN not available — nftables and NETLINK_SOCK_DIAG socket destroy are disabled (check systemd AmbientCapabilities)")
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
	cleanup := &nodeComponentCleanup{
		stopNetwork:     system.DefaultNetworkMonitor().Stop,
		shutdownManager: manager.Shutdown,
		stopCore: func() error {
			if response := manager.Stop(); !response.IsStopped {
				return errors.New("process did not stop")
			}
			return nil
		},
		closePlugin: pluginService.CloseContext,
	}
	cleanupComponents := cleanup.Run
	cleanupCompleted := false
	defer func() {
		if !cleanupCompleted {
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), nodeShutdownTimeout)
			runErr = errors.Join(runErr, cleanupComponents(cleanupCtx))
			cancelCleanup()
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
	startServer("HTTPS server", func() error { return server.ListenAndServeTLS(ctx) })

	servers.Add(1)
	go func() {
		defer servers.Done()
		manager.StartLogRotation(ctx)
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		runErr = errors.Join(runErr, err)
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), nodeShutdownTimeout)
	defer cancel()
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- cleanupComponents(shutdownCtx) }()
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
	runErr = errors.Join(runErr, <-cleanupDone)
	cleanupCompleted = true
	for {
		select {
		case err := <-serveErrors:
			runErr = errors.Join(runErr, err)
		default:
			return runErr
		}
	}
}

func runtimeEnvPath() string {
	if path := strings.TrimSpace(os.Getenv("REMNANODE_ENV")); path != "" {
		return path
	}
	return config.ResolveEnvPath()
}

func applyRuntimeOverrides(cfg config.Config) error {
	for key, value := range map[string]string{
		"NODE_CONTRACT_VERSION": cfg.NodeContractVersion,
		"XRAY_CORE_VERSION":     cfg.XrayCoreVersion,
	} {
		if value == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s runtime override: %w", key, err)
		}
	}
	return nil
}

// applyMemoryLimit runs after the bounded config read and before Secret parsing
// or server construction. Explicit GOMEMLIMIT data wins over LOW_MEMORY.
func applyMemoryLimit(cfg config.Config) {
	if cfg.GoMemoryLimitSet {
		debug.SetMemoryLimit(cfg.GoMemoryLimitBytes)
		return
	}
	if cfg.LowMemory {
		debug.SetMemoryLimit(180 << 20)
		log.Printf("low-memory mode: Go soft memory limit set to 180MiB (override with GOMEMLIMIT)")
	}
}
