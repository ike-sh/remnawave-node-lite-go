package config

import (
	"os"
	"path/filepath"
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
	if cfg.XrayBin != defaultXrayBin || cfg.GeoDir != defaultGeoDir || cfg.GeocheckBin != defaultGeocheckBin {
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

func TestLoadGeocheckOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NODE_PORT=3000\nSECRET_KEY=abc\nGEOCHECK_BIN=/opt/geocheck\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.GeocheckBin != "/opt/geocheck" {
		t.Fatalf("GeocheckBin = %q, want /opt/geocheck", cfg.GeocheckBin)
	}
}

func TestSNIAndNFTablesOfficialBooleanDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, extra                     string
		wantSNI, wantLogging, wantReply bool
	}{
		{"old-v1.3-env", "", false, true, false},
		{"explicit-false", "SNI_VERIFICATION=false\n", false, true, false},
		{"explicit-true", "SNI_VERIFICATION=true\n", true, true, false},
		{"nft-options", "NFTABLES_LOGGING=false\nNFTABLES_ACCEPT_REPLY_TRAFFIC=true\n", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"SNI_VERIFICATION", "NFTABLES_LOGGING", "NFTABLES_ACCEPT_REPLY_TRAFFIC"} {
				t.Setenv(key, "")
			}
			path := filepath.Join(t.TempDir(), "node.env")
			original := "NODE_PORT=4321\nSECRET_KEY=preserved-key\nCUSTOM_USER_VAR=keep-me\n" + tc.extra
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.SNIVerification != tc.wantSNI || cfg.NFTablesLogging != tc.wantLogging || cfg.NFTablesAcceptReplyTraffic != tc.wantReply {
				t.Fatalf("unexpected flags: %#v", cfg)
			}
			if cfg.NodePort != 4321 || cfg.SecretKey != "preserved-key" {
				t.Fatalf("existing settings lost: %#v", cfg)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != original {
				t.Fatal("loading changed node.env")
			}
		})
	}
}

func TestSNIRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("SNI_VERIFICATION", "")
	path := filepath.Join(t.TempDir(), "node.env")
	if err := os.WriteFile(path, []byte("NODE_PORT=3000\nSECRET_KEY=abc\nSNI_VERIFICATION=yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("official boolean schema must reject yes")
	}
}
