package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/config"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/secret"
	"golang.org/x/sys/unix"
)

func TestValidateSecretCommand(t *testing.T) {
	validJSON := `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`
	valid := base64.StdEncoding.EncodeToString([]byte(validJSON))
	invalidJSON := base64.StdEncoding.EncodeToString([]byte(validJSON + `trailing}`))

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "valid", input: valid, want: 0},
		{name: "invalid json", input: invalidJSON, want: 1},
		{name: "oversized", input: strings.Repeat("A", secret.MaxEncodedBytes+1), want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := validateSecretCommand(strings.NewReader(test.input), &stderr); got != test.want {
				t.Fatalf("validateSecretCommand() = %d, want %d; stderr=%q", got, test.want, stderr.String())
			}
		})
	}
}

func TestValidateSecretCommandAcceptsExactLimitWithLineEnding(t *testing.T) {
	const prefix = `{"caCertPem":"`
	const suffix = `","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`
	rawLength := base64.StdEncoding.DecodedLen(secret.MaxEncodedBytes)
	raw := prefix + strings.Repeat("a", rawLength-len(prefix)-len(suffix)) + suffix
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	if len(encoded) != secret.MaxEncodedBytes {
		t.Fatalf("encoded length = %d, want %d", len(encoded), secret.MaxEncodedBytes)
	}

	var stderr bytes.Buffer
	if code := validateSecretCommand(strings.NewReader(encoded+"\r\n"), &stderr); code != 0 {
		t.Fatalf("validateSecretCommand() = %d, want 0; stderr=%q", code, stderr.String())
	}
}

func TestCanonicalizeSecretCommand(t *testing.T) {
	t.Parallel()

	validJSON := `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`
	canonical := base64.StdEncoding.EncodeToString([]byte(validJSON))
	source := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(source, []byte(canonical+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		path  string
		stdin string
	}{
		{name: "file", path: source},
		{name: "stdin", path: "-", stdin: canonical + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := canonicalizeSecretCommand(test.path, strings.NewReader(test.stdin), &stdout, &stderr); code != 0 {
				t.Fatalf("canonicalizeSecretCommand() = %d, stderr=%q", code, stderr.String())
			}
			if got := stdout.String(); got != canonical {
				t.Fatalf("canonical output = %q, want %q", got, canonical)
			}
		})
	}
}

func TestCanonicalizeSecretCommandRejectsUnsafeFileWithoutOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	validJSON := `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`
	canonical := base64.StdEncoding.EncodeToString([]byte(validJSON))
	regular := filepath.Join(dir, "regular.key")
	if err := os.WriteFile(regular, []byte(canonical), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "symlink.key")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := canonicalizeSecretCommand(symlink, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("symlink Secret Key source unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed command leaked canonical secret to stdout: %q", stdout.String())
	}

	fifo := filepath.Join(dir, "secret.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		var fifoStdout, fifoStderr bytes.Buffer
		done <- canonicalizeSecretCommand(fifo, strings.NewReader(""), &fifoStdout, &fifoStderr)
	}()
	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("FIFO Secret Key source unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO Secret Key source blocked instead of failing closed")
	}
}

func TestCanonicalizeSecretCommandReportsOutputFailure(t *testing.T) {
	t.Parallel()

	validJSON := `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`
	canonical := base64.StdEncoding.EncodeToString([]byte(validJSON))
	var stderr bytes.Buffer
	if code := canonicalizeSecretCommand("-", strings.NewReader(canonical), errorWriter{}, &stderr); code == 0 {
		t.Fatal("output failure unexpectedly succeeded")
	}
	if !strings.Contains(stderr.String(), "write canonical SECRET_KEY") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestApplyRuntimeOverridesUsesValidatedConfigValues(t *testing.T) {
	t.Setenv("NODE_CONTRACT_VERSION", "")
	t.Setenv("XRAY_CORE_VERSION", "")

	cfg := config.Config{
		NodeContractVersion: "2.8.0-test",
		XrayCoreVersion:     "v26.6.27",
	}
	if err := applyRuntimeOverrides(cfg); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("NODE_CONTRACT_VERSION"); got != cfg.NodeContractVersion {
		t.Fatalf("NODE_CONTRACT_VERSION = %q, want %q", got, cfg.NodeContractVersion)
	}
	if got := os.Getenv("XRAY_CORE_VERSION"); got != cfg.XrayCoreVersion {
		t.Fatalf("XRAY_CORE_VERSION = %q, want %q", got, cfg.XrayCoreVersion)
	}
}

func TestRuntimeEnvPathHonorsFixedLauncherPath(t *testing.T) {
	t.Setenv("REMNANODE_ENV", "/etc/remnanode/node.env")
	if got := runtimeEnvPath(); got != "/etc/remnanode/node.env" {
		t.Fatalf("runtimeEnvPath() = %q", got)
	}
}
