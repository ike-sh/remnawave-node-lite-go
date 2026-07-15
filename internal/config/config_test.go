package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnvWithDefaults(t *testing.T) {
	t.Setenv("NODE_PORT", "")
	t.Setenv("SECRET_KEY", "")
	t.Setenv("XRAY_BIN", "")
	t.Setenv("GEO_DIR", "")
	for _, key := range []string{"NODE_PORT", "SECRET_KEY", "XRAY_BIN", "GEO_DIR"} {
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
	if cfg.XrayBin != defaultXrayBin || cfg.GeoDir != defaultGeoDir {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.LogDir != defaultLogDir {
		t.Fatalf("unexpected default LOG_DIR: %s (want %s)", cfg.LogDir, defaultLogDir)
	}
	if cfg.InternalSocketPath != defaultInternalSocketPath || cfg.InternalRESTToken == "" {
		t.Fatalf("unexpected internal defaults: %#v", cfg)
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

func TestLoadSecretFromFile(t *testing.T) {
	t.Setenv("SECRET_KEY", "")
	t.Setenv("SECRET_KEY_FILE", "")
	os.Unsetenv("SECRET_KEY")
	os.Unsetenv("SECRET_KEY_FILE")

	secretPath := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(secretPath, []byte("file-secret-key\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	path := filepath.Join(t.TempDir(), ".env")
	content := "NODE_PORT=3000\nSECRET_KEY_FILE=" + secretPath + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SecretKey != "file-secret-key" {
		t.Fatalf("unexpected secret from file: %q", cfg.SecretKey)
	}
}

func TestLoadSecretKeyOverridesFile(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(secretPath, []byte("from-file"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	path := filepath.Join(t.TempDir(), ".env")
	content := "NODE_PORT=3000\nSECRET_KEY=inline\nSECRET_KEY_FILE=" + secretPath + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("SECRET_KEY", "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SecretKey != "inline" {
		t.Fatalf("SECRET_KEY should override file, got %q", cfg.SecretKey)
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

func TestLoadStrictOptionalValues(t *testing.T) {
	for _, key := range []string{"LOW_MEMORY", "BODY_LIMIT_MB", "DISABLE_HASHED_SET_CHECK"} {
		t.Setenv(key, "")
	}
	path := filepath.Join(t.TempDir(), ".env")
	content := "NODE_PORT=3000\nSECRET_KEY=abc\nLOW_MEMORY=YES\nDISABLE_HASHED_SET_CHECK=no\nBODY_LIMIT_MB=16\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.LowMemory || cfg.DisableHashedSetCheck || cfg.BodyLimitMB != 16 {
		t.Fatalf("unexpected optional values: %#v", cfg)
	}
}

func TestLoadRejectsInvalidOptionalValues(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantError string
	}{
		{name: "low memory boolean", line: "LOW_MEMORY=enabled", wantError: "LOW_MEMORY must be a boolean"},
		{name: "hash check boolean", line: "DISABLE_HASHED_SET_CHECK=disabled", wantError: "DISABLE_HASHED_SET_CHECK must be a boolean"},
		{name: "body limit text", line: "BODY_LIMIT_MB=large", wantError: "BODY_LIMIT_MB must be an integer"},
		{name: "body limit integer overflow", line: "BODY_LIMIT_MB=999999999999999999999999", wantError: "BODY_LIMIT_MB must be an integer"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"LOW_MEMORY", "BODY_LIMIT_MB", "DISABLE_HASHED_SET_CHECK"} {
				t.Setenv(key, "")
			}
			path := filepath.Join(t.TempDir(), ".env")
			content := "NODE_PORT=3000\nSECRET_KEY=abc\n" + test.line + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Load error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}
