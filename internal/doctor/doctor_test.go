package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"remnawave-node-lite-go/internal/config"
)

func TestRunMissingEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.env")
	if code := Run([]string{"--env", missing}); code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestCheckSecret(t *testing.T) {
	t.Parallel()
	if r := checkSecret(config.Config{SecretKey: "x"}); r[0].level != "ERROR" {
		t.Fatalf("expected malformed Secret Key error, got %#v", r)
	}
	if r := checkSecret(config.Config{}); r[0].level != "ERROR" {
		t.Fatalf("expected ERROR, got %#v", r)
	}
}

func TestCheckGeocheckBinary(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("executable permission bits are a Linux deployment property")
	}
	path := filepath.Join(t.TempDir(), "geocheck")
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := checkGeocheckBinary(path); r[0].level != "OK" {
		t.Fatalf("expected executable GeoCheck to pass, got %#v", r)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	t.Parallel()
	envPath := filepath.Join(t.TempDir(), "node.env")
	if err := os.WriteFile(envPath, []byte("SECRET_KEY=abc\nNODE_PORT=2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SecretKey != "abc" || cfg.NodePort != 2222 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
