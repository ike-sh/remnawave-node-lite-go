package xray

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"remnawave-node-lite-go/internal/artifact"
)

func TestBuildCommandArgs(t *testing.T) {
	args := BuildCommandArgs("/run/remnawave.sock")

	if len(args) != 4 {
		t.Fatalf("unexpected args: %#v", args)
	}
	if args[0] != "-config" || args[2] != "-format" || args[3] != "json" {
		t.Fatalf("unexpected args: %#v", args)
	}
	if got := args[1]; got != "http+unix:///run/remnawave.sock/internal/get-config" {
		t.Fatalf("unexpected config URL: %s", got)
	}
}

func TestStartPreparesGeodataBeforeSpawningCore(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("geo"))
	}))
	defer server.Close()
	geoDir := t.TempDir()
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             geoDir,
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.geodataLoader.download = func(ctx context.Context, rawURL, path string, opts artifact.Options) (artifact.Result, error) {
		opts.Client = server.Client()
		return artifact.Download(ctx, rawURL, path, opts)
	}
	response := manager.Start(context.Background(), StartRequest{XrayConfig: map[string]any{
		"geodata": map[string]any{
			"assets": []any{map[string]any{"url": server.URL, "file": "custom.dat"}},
		},
	}})
	if response.IsStarted {
		t.Fatal("missing core should not start")
	}
	if got, err := os.ReadFile(filepath.Join(geoDir, "custom.dat")); err != nil || string(got) != "geo" {
		t.Fatalf("geodata was not prepared before spawn: %q, %v", got, err)
	}
}

func TestGenerateAPIConfigInjectsRemnawaveAPI(t *testing.T) {
	config := generateAPIConfig(map[string]any{
		"inbounds": []any{map[string]any{"tag": "public"}},
		"routing": map[string]any{
			"rules": []any{map[string]any{"outboundTag": "direct"}},
		},
	}, "remnanode-xtls-test", TorrentBlockerOptions{})

	inbounds, ok := config["inbounds"].([]any)
	if !ok || len(inbounds) != 2 {
		t.Fatalf("expected API inbound plus original inbound, got %#v", config["inbounds"])
	}
	apiInbound, ok := inbounds[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected API inbound type: %#v", inbounds[0])
	}
	if apiInbound["tag"] != apiInboundTag || apiInbound["listen"] != "@remnanode-xtls-test" || apiInbound["protocol"] != "tunnel" {
		t.Fatalf("unexpected API inbound: %#v", apiInbound)
	}
	if _, ok := config["stats"].(map[string]any); !ok {
		t.Fatalf("expected stats object")
	}

	api, ok := config["api"].(map[string]any)
	if !ok || api["tag"] != apiTag {
		t.Fatalf("expected API model, got %#v", config["api"])
	}

	routing, ok := config["routing"].(map[string]any)
	if !ok {
		t.Fatalf("expected routing object")
	}
	rules, ok := routing["rules"].([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("expected injected routing rule plus original rules, got %#v", routing["rules"])
	}
}

func TestStartStoresFullConfigWhenCommandFails(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             "/tmp",
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token"})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	response := manager.Start(context.Background(), StartRequest{
		XrayConfig: map[string]any{
			"inbounds": []any{"one"},
		},
	})

	if response.IsStarted {
		t.Fatal("start with missing rw-core must not report Xray as started")
	}
	if response.Error == nil || !strings.Contains(*response.Error, "start rw-core") {
		t.Fatalf("expected start error, got %#v", response.Error)
	}

	config := manager.CurrentConfig()
	inbounds, ok := config["inbounds"].([]any)
	if !ok || len(inbounds) != 2 {
		t.Fatalf("expected generated config to be saved, got %#v", config)
	}
}

func TestStartRejectsWhileLifecycleOperationIsActive(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin: "definitely-missing-rw-core",
		GeoDir:  t.TempDir(),
		LogDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.lifecycleMu.Lock()
	response := manager.Start(context.Background(), StartRequest{XrayConfig: map[string]any{}})
	manager.lifecycleMu.Unlock()
	if response.Error == nil || *response.Error != "Request already in progress" {
		t.Fatalf("unexpected response while lifecycle is busy: %#v", response)
	}
}

func TestWaitForGRPCFailsImmediatelyWhenProcessExited(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin: "definitely-missing-rw-core",
		GeoDir:  t.TempDir(),
		LogDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now()
	if manager.waitForGRPC(context.Background(), time.Second) {
		t.Fatal("missing process must not become ready")
	}
	if elapsed := time.Since(startedAt); elapsed > 250*time.Millisecond {
		t.Fatalf("missing process waited too long: %s", elapsed)
	}
}

func TestStopIfOnlineAlsoStopsInProgressLifecycle(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin: "definitely-missing-rw-core",
		GeoDir:  t.TempDir(),
		LogDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.startProcessing = true
	manager.mu.Unlock()

	if !manager.StopIfOnline() {
		t.Fatal("in-progress lifecycle should be stopped even before online state is set")
	}
}

func TestStopClearsConfig(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             "/tmp",
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token"})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.Start(context.Background(), StartRequest{XrayConfig: map[string]any{"a": "b"}})
	manager.mu.Lock()
	manager.emptyConfigHash = "stale"
	manager.inboundHashes = map[string]*HashedSet{"inbound": NewHashedSet("user")}
	manager.inboundTags = map[string]struct{}{"inbound": {}}
	manager.mu.Unlock()
	manager.Stop(true)

	if len(manager.CurrentConfig()) != 0 {
		t.Fatalf("expected config to be cleared")
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.emptyConfigHash != "" || manager.inboundHashes != nil || manager.inboundTags != nil {
		t.Fatalf("runtime hash state survived stop without a process")
	}
}

func TestCurrentConfigJSONCachedAndCleared(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             "/tmp",
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token"})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if got := string(manager.CurrentConfigJSON()); got != "{}" {
		t.Fatalf("expected empty object before start, got %s", got)
	}

	manager.Start(context.Background(), StartRequest{XrayConfig: map[string]any{
		"inbounds": []any{map[string]any{"tag": "public"}},
	}})

	raw := manager.CurrentConfigJSON()
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("cached JSON must be valid: %v", err)
	}
	if _, ok := decoded["inbounds"]; !ok {
		t.Fatalf("expected inbounds in cached JSON, got %s", raw)
	}

	manager.Stop(true)
	if got := string(manager.CurrentConfigJSON()); got != "{}" {
		t.Fatalf("expected cache cleared after stop, got %s", got)
	}
}

func TestStopWithoutClearPersistKeepsLastStart(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             "/tmp",
		LogDir:             t.TempDir(),
		DataDir:            dir,
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token"})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	req := StartRequest{XrayConfig: map[string]any{"a": "b"}}
	if err := savePersistedStart(dir, req); err != nil {
		t.Fatalf("save: %v", err)
	}

	manager.Stop(false)

	if _, err := os.Stat(filepath.Join(dir, persistedStartFile)); err != nil {
		t.Fatalf("expected persisted file to remain after shutdown stop: %v", err)
	}
}

func TestStopFalseFlushesLastStartRequest(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             "/tmp",
		LogDir:             t.TempDir(),
		DataDir:            dir,
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token"})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	req := StartRequest{
		XrayConfig: map[string]any{"inbounds": []any{"one"}},
		Internals: StartInternals{
			Hashes: ConfigHash{EmptyConfig: "abc"},
		},
	}
	manager.persistStartRequest(req)
	if err := os.Remove(filepath.Join(dir, persistedStartFile)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove persisted file: %v", err)
	}

	manager.Stop(false)

	if _, err := os.Stat(filepath.Join(dir, persistedStartFile)); err != nil {
		t.Fatalf("expected shutdown flush to recreate persisted file: %v", err)
	}
}

func TestParseVersionLine(t *testing.T) {
	raw := "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)\nA unified platform..."
	if got := parseVersionLine(raw); got != "26.3.27" {
		t.Fatalf("parseVersionLine() = %q, want 26.3.27", got)
	}

	t.Setenv("XRAY_CORE_VERSION", "v26.3.27")
	if got := parseVersionLine("Xray 26.7.28"); got != "26.7.28" {
		t.Fatalf("binary output must win over stale XRAY_CORE_VERSION, got %q", got)
	}
}
