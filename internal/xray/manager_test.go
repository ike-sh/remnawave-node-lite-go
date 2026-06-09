package xray

import (
	"context"
	"strings"
	"testing"
)

func TestBuildCommandArgs(t *testing.T) {
	args := BuildCommandArgs("/run/remnawave.sock", "token value")

	if len(args) != 4 {
		t.Fatalf("unexpected args: %#v", args)
	}
	if args[0] != "-config" || args[2] != "-format" || args[3] != "json" {
		t.Fatalf("unexpected args: %#v", args)
	}
	if got := args[1]; got != "http+unix:///run/remnawave.sock/internal/get-config?token=token+value" {
		t.Fatalf("unexpected config URL: %s", got)
	}
}

func TestGenerateAPIConfigInjectsRemnawaveAPI(t *testing.T) {
	certs, err := generateInternalCerts()
	if err != nil {
		t.Fatalf("generateInternalCerts: %v", err)
	}

	config := generateAPIConfig(map[string]any{
		"inbounds": []any{map[string]any{"tag": "public"}},
		"routing": map[string]any{
			"rules": []any{map[string]any{"outboundTag": "direct"}},
		},
	}, 61000, certs, TorrentBlockerOptions{})

	inbounds, ok := config["inbounds"].([]any)
	if !ok || len(inbounds) != 2 {
		t.Fatalf("expected API inbound plus original inbound, got %#v", config["inbounds"])
	}
	apiInbound, ok := inbounds[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected API inbound type: %#v", inbounds[0])
	}
	if apiInbound["tag"] != apiInboundTag || apiInbound["listen"] != "127.0.0.1" || apiInbound["port"] != 61000 {
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
		InternalRESTToken:  "token",
		XtlsAPIPort:        61000,
	})
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

func TestStopClearsConfig(t *testing.T) {
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             "/tmp",
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token",
		XtlsAPIPort:        61000,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.Start(context.Background(), StartRequest{XrayConfig: map[string]any{"a": "b"}})
	manager.Stop()

	if len(manager.CurrentConfig()) != 0 {
		t.Fatalf("expected config to be cleared")
	}
}

func TestParseVersionLine(t *testing.T) {
	raw := "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/amd64)\nA unified platform..."
	if got := parseVersionLine(raw); got != "26.3.27" {
		t.Fatalf("parseVersionLine() = %q, want 26.3.27", got)
	}

	t.Setenv("XRAY_CORE_VERSION", "v26.3.27")
	if got := parseVersionLine("ignored"); got != "26.3.27" {
		t.Fatalf("XRAY_CORE_VERSION override = %q, want 26.3.27", got)
	}
}
