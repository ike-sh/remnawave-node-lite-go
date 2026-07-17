package doctor

import (
	"bytes"
	"encoding/base64"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/asn"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/config"
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
	valid := base64.StdEncoding.EncodeToString([]byte(`{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`))
	if r := checkSecret(config.Config{SecretKey: valid}); r[0].level != "OK" {
		t.Fatalf("expected OK, got %#v", r)
	}
	if r := checkSecret(config.Config{}); r[0].level != "ERROR" {
		t.Fatalf("expected ERROR, got %#v", r)
	}

	const marker = "secret-material-must-not-leak"
	invalid := base64.StdEncoding.EncodeToString([]byte(`{"caCertPem":"` + marker + `"}`))
	r := checkSecret(config.Config{SecretKey: invalid})[0]
	if r.level != "ERROR" {
		t.Fatalf("invalid secret level = %q, want ERROR", r.level)
	}
	visible := r.detail + r.fixHint
	if strings.Contains(visible, marker) || strings.Contains(visible, invalid) {
		t.Fatalf("secret diagnostic leaked input: %q", visible)
	}
}

func TestCheckASNDatabaseUsesRuntimeReader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.bin")
	if r := checkASNDatabase(missing)[0]; r.level != "WARN" || !strings.Contains(r.detail, "降级为空") {
		t.Fatalf("missing database result = %#v", r)
	}

	invalid := filepath.Join(dir, "invalid.bin")
	if err := os.WriteFile(invalid, []byte(strings.Repeat("x", 16)), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := checkASNDatabase(invalid)[0]; r.level != "WARN" || !strings.Contains(r.detail, "invalid asn database magic") {
		t.Fatalf("invalid database result = %#v", r)
	}

	empty := filepath.Join(dir, "empty.bin")
	writeASNDatabase(t, empty, nil)
	if r := checkASNDatabase(empty)[0]; r.level != "WARN" || !strings.Contains(r.detail, "不含 ASN 条目") {
		t.Fatalf("empty database result = %#v", r)
	}

	valid := filepath.Join(dir, "valid.bin")
	writeASNDatabase(t, valid, []asn.Entry{{
		ASN:  64512,
		IPv4: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
	}})
	if r := checkASNDatabase(valid)[0]; r.level != "OK" {
		t.Fatalf("valid database result = %#v", r)
	}
}

func writeASNDatabase(t *testing.T, path string, entries []asn.Entry) {
	t.Helper()
	var output bytes.Buffer
	if err := asn.Write(&output, entries); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
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
