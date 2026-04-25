package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvWithDefaults(t *testing.T) {
	t.Setenv("NODE_PORT", "")
	t.Setenv("SECRET_KEY", "")
	t.Setenv("XTLS_API_PORT", "")
	t.Setenv("XRAY_BIN", "")
	t.Setenv("GEO_DIR", "")
	for _, key := range []string{"NODE_PORT", "SECRET_KEY", "XTLS_API_PORT", "XRAY_BIN", "GEO_DIR"} {
		os.Unsetenv(key)
	}

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NODE_PORT=3000\nSECRET_KEY=abc\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.NodePort != 3000 {
		t.Fatalf("unexpected NODE_PORT: %d", cfg.NodePort)
	}
	if cfg.XtlsAPIPort != 61000 {
		t.Fatalf("unexpected default XTLS_API_PORT: %d", cfg.XtlsAPIPort)
	}
	if cfg.XrayBin != defaultXrayBin || cfg.GeoDir != defaultGeoDir {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.LogDir != defaultLogDir {
		t.Fatalf("unexpected default LOG_DIR: %s", cfg.LogDir)
	}
	if cfg.InternalSocketPath == "" || cfg.InternalRESTToken == "" {
		t.Fatalf("expected generated internal socket path and token: %#v", cfg)
	}
}

func TestLoadEnvironmentOverridesDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NODE_PORT=3000\nSECRET_KEY=abc\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("NODE_PORT", "4000")
	t.Setenv("SECRET_KEY", "from-env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.NodePort != 4000 || cfg.SecretKey != "from-env" {
		t.Fatalf("environment did not override .env: %#v", cfg)
	}
}

func TestLoadInternalOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NODE_PORT=3000\nSECRET_KEY=abc\nINTERNAL_SOCKET_PATH=/tmp/node.sock\nINTERNAL_REST_TOKEN=token\nLOG_DIR=/tmp/logs\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.InternalSocketPath != "/tmp/node.sock" || cfg.InternalRESTToken != "token" || cfg.LogDir != "/tmp/logs" {
		t.Fatalf("unexpected internal config: %#v", cfg)
	}
}
