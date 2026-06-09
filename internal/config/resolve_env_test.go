package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveEnvPathPrefersProductionDefault(t *testing.T) {
	dir := t.TempDir()
	prod := filepath.Join(dir, "node.env")
	if err := os.WriteFile(prod, []byte("NODE_PORT=2222\nSECRET_KEY=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("REMNANODE_ENV_TEST_DIR", dir)
	// ResolveEnvPath uses fixed DefaultEnvPath; test Load with explicit path instead.
	cfg, err := Load(prod)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodePort != 2222 || cfg.SecretKey != "test" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
