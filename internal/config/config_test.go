package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/secret"
	"golang.org/x/sys/unix"
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

func TestHTTPAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "all interfaces", host: "", want: ":2222"},
		{name: "IPv4", host: "127.0.0.1", want: "127.0.0.1:2222"},
		{name: "hostname", host: "localhost", want: "localhost:2222"},
		{name: "IPv6", host: "::1", want: "[::1]:2222"},
		{name: "bracketed IPv6", host: "[::1]", want: "[::1]:2222"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{BindAddr: test.host, NodePort: 2222}
			if got := cfg.HTTPAddr(); got != test.want {
				t.Fatalf("HTTPAddr() = %q, want %q", got, test.want)
			}
		})
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

func TestParseDotEnvRejectsUnboundedOrSpecialFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	regular := filepath.Join(dir, "regular.env")
	if err := os.WriteFile(regular, []byte("NODE_PORT=2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	symlink := filepath.Join(dir, "symlink.env")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDotEnv(symlink); err == nil {
		t.Fatal("expected symlink env file to fail")
	}

	fifo := filepath.Join(dir, "node.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDotEnv(fifo); err == nil {
		t.Fatal("expected FIFO env file to fail without opening it")
	}

	oversized := filepath.Join(dir, "oversized.env")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("A", maxDotEnvBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseDotEnv(oversized); err == nil {
		t.Fatal("expected oversized env file to fail")
	}
}

func TestSecureOpenKeepsNonblockingDescriptor(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "node.env")
	if err := os.WriteFile(path, []byte("NODE_PORT=2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openReadOnlyNoFollow(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatal("secure file descriptor does not retain O_NONBLOCK")
	}
}

func TestParseDotEnvBoundsLinesAndAssignments(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		content string
	}{
		{
			name:    "lines",
			content: strings.Repeat("#\n", maxDotEnvLines+1),
		},
		{
			name: "assignments",
			content: func() string {
				var content strings.Builder
				for index := 0; index <= maxDotEnvAssignments; index++ {
					fmt.Fprintf(&content, "KEY_%d=value\n", index)
				}
				return content.String()
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "node.env")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := parseDotEnv(path); err == nil {
				t.Fatalf("expected %s limit to fail", test.name)
			}
		})
	}
}

func TestParseDotEnvAcceptsBoundedLargeInlineValue(t *testing.T) {
	t.Parallel()

	want := strings.Repeat("A", secret.MaxEncodedBytes)
	path := filepath.Join(t.TempDir(), "node.env")
	if err := os.WriteFile(path, []byte("SECRET_KEY="+want+"\nNODE_PORT=2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := parseDotEnv(path)
	if err != nil {
		t.Fatalf("parseDotEnv large inline value: %v", err)
	}
	if values["SECRET_KEY"] != want {
		t.Fatalf("SECRET_KEY length = %d, want %d", len(values["SECRET_KEY"]), len(want))
	}
}

func TestLoadSecretFromFileRejectsUnsafeInputs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	regular := filepath.Join(dir, "regular.key")
	if err := os.WriteFile(regular, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	oversized := filepath.Join(dir, "oversized.key")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("A", secret.MaxEncodedBytes+3)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFromFile(map[string]string{"SECRET_KEY_FILE": oversized}); err == nil {
		t.Fatal("expected oversized secret file to fail")
	}

	symlink := filepath.Join(dir, "symlink.key")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFromFile(map[string]string{"SECRET_KEY_FILE": symlink}); err == nil {
		t.Fatal("expected symlink secret file to fail")
	}

	fifo := filepath.Join(dir, "secret.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretFromFile(map[string]string{"SECRET_KEY_FILE": fifo}); err == nil {
		t.Fatal("expected FIFO secret file to fail without opening it")
	}

	boundary := filepath.Join(dir, "boundary.key")
	want := strings.Repeat("A", secret.MaxEncodedBytes)
	if err := os.WriteFile(boundary, []byte(want+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadSecretFromFile(map[string]string{"SECRET_KEY_FILE": boundary})
	if err != nil {
		t.Fatalf("load exact boundary with CRLF: %v", err)
	}
	if got != want {
		t.Fatalf("canonical boundary length = %d, want %d", len(got), len(want))
	}
}

func TestCanonicalSecretFileSuffix(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "none", input: "YWJj", want: "YWJj"},
		{name: "LF", input: "YWJj\n", want: "YWJj"},
		{name: "CRLF", input: "YWJj\r\n", want: "YWJj"},
		{name: "URL safe", input: "-_8=", want: "-_8="},
		{name: "leading space", input: " YWJj", wantErr: true},
		{name: "internal tab", input: "YW\tJj", wantErr: true},
		{name: "double LF", input: "YWJj\n\n", wantErr: true},
		{name: "bare CR", input: "YWJj\r", wantErr: true},
		{name: "empty", input: "\n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := canonicalSecretFile([]byte(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatalf("canonicalSecretFile(%q) unexpectedly succeeded", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("canonicalSecretFile(%q): %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("canonicalSecretFile(%q) = %q, want %q", test.input, got, test.want)
			}
		})
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
	for _, key := range []string{
		"LOW_MEMORY", "BODY_LIMIT_MB", "DISABLE_HASHED_SET_CHECK", "GOMEMLIMIT",
		"NODE_CONTRACT_VERSION", "XRAY_CORE_VERSION",
	} {
		t.Setenv(key, "")
	}
	path := filepath.Join(t.TempDir(), ".env")
	content := "NODE_PORT=3000\nSECRET_KEY=abc\nLOW_MEMORY=YES\nDISABLE_HASHED_SET_CHECK=no\n" +
		"BODY_LIMIT_MB=16\nGOMEMLIMIT=180MiB\nNODE_CONTRACT_VERSION=2.8.0\nXRAY_CORE_VERSION=v26.6.27\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.LowMemory || cfg.DisableHashedSetCheck || cfg.BodyLimitMB != 16 ||
		!cfg.GoMemoryLimitSet || cfg.GoMemoryLimitBytes != 180<<20 ||
		cfg.NodeContractVersion != "2.8.0" || cfg.XrayCoreVersion != "v26.6.27" {
		t.Fatalf("unexpected optional values: %#v", cfg)
	}
}

func TestOptionalMemoryLimit(t *testing.T) {
	t.Parallel()

	maxInt64 := int64(^uint64(0) >> 1)
	for _, test := range []struct {
		raw     string
		want    int64
		wantSet bool
		wantErr bool
	}{
		{raw: "", wantSet: false},
		{raw: "off", want: maxInt64, wantSet: true},
		{raw: "123456", want: 123456, wantSet: true},
		{raw: "188743680", want: 180 << 20, wantSet: true},
		{raw: "180MiB", want: 180 << 20, wantSet: true},
		{raw: "1TiB", want: 1 << 40, wantSet: true},
		{raw: "180MB", wantErr: true},
		{raw: "9223372036854775808", wantErr: true},
		{raw: "-1MiB", wantErr: true},
	} {
		got, set, err := optionalMemoryLimit(map[string]string{"GOMEMLIMIT": test.raw}, "GOMEMLIMIT")
		if (err != nil) != test.wantErr {
			t.Errorf("optionalMemoryLimit(%q) error = %v, wantErr %v", test.raw, err, test.wantErr)
			continue
		}
		if err == nil && (got != test.want || set != test.wantSet) {
			t.Errorf("optionalMemoryLimit(%q) = (%d, %v), want (%d, %v)", test.raw, got, set, test.want, test.wantSet)
		}
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
		{name: "memory limit suffix", line: "GOMEMLIMIT=180MB", wantError: "GOMEMLIMIT must be"},
		{name: "contract version", line: "NODE_CONTRACT_VERSION=latest", wantError: "NODE_CONTRACT_VERSION has an invalid"},
		{name: "core version", line: "XRAY_CORE_VERSION=26", wantError: "XRAY_CORE_VERSION has an invalid"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{
				"LOW_MEMORY", "BODY_LIMIT_MB", "DISABLE_HASHED_SET_CHECK", "GOMEMLIMIT",
				"NODE_CONTRACT_VERSION", "XRAY_CORE_VERSION",
			} {
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
