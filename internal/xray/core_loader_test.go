package xray

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"remnawave-node-lite-go/internal/artifact"
)

func TestParseCoreSpecRequiresHTTPSAndSHA256(t *testing.T) {
	validSHA := strings.Repeat("a", 64)
	spec, present, err := parseCoreSpec(map[string]any{
		"core": map[string]any{"url": "https://example.com/core", "sha256": validSHA},
	})
	if err != nil || !present || spec.SHA256 != validSHA {
		t.Fatalf("valid core spec rejected: spec=%#v present=%v err=%v", spec, present, err)
	}
	if _, _, err := parseCoreSpec(map[string]any{
		"core": map[string]any{"url": "http://example.com/core", "sha256": validSHA},
	}); err == nil {
		t.Fatal("expected HTTP core URL to be rejected")
	}
	if _, _, err := parseCoreSpec(map[string]any{
		"core": map[string]any{"url": "https://example.com/core", "sha256": "missing"},
	}); err == nil {
		t.Fatal("expected invalid digest to be rejected")
	}
}

func TestCoreLoaderInstallsOnceAndRestoresStock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link activation is a Linux runtime behavior")
	}
	payload := []byte("custom core")
	digest := sha256.Sum256(payload)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	dir := t.TempDir()
	stock := filepath.Join(dir, "xray")
	active := filepath.Join(dir, "rw-core")
	if err := os.WriteFile(stock, []byte("stock"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stock, active); err != nil {
		t.Fatal(err)
	}
	loader := newCoreLoader(active)
	loader.download = func(ctx context.Context, rawURL, path string, opts artifact.Options) (artifact.Result, error) {
		opts.Client = server.Client()
		return artifact.Download(ctx, rawURL, path, opts)
	}
	loader.readVersion = func(context.Context, string) (string, error) {
		return "Xray 26.7.28 test", nil
	}
	config := map[string]any{
		"core": map[string]any{"url": server.URL, "sha256": hex.EncodeToString(digest[:])},
	}
	if err := loader.prepare(context.Background(), config); err != nil {
		t.Fatalf("prepare custom core: %v", err)
	}
	if err := loader.prepare(context.Background(), config); err != nil {
		t.Fatalf("prepare installed core: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("core downloaded %d times, want 1", requests.Load())
	}
	target, err := os.Readlink(active)
	if err != nil || resolveLink(active, target) != loader.paths.custom {
		t.Fatalf("active link = %q, err=%v", target, err)
	}
	if err := os.Chmod(loader.paths.custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loader.prepare(context.Background(), config); err != nil {
		t.Fatalf("repair non-executable custom core: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("non-executable core was not replaced; downloads=%d", requests.Load())
	}

	if err := loader.prepare(context.Background(), map[string]any{}); err != nil {
		t.Fatalf("restore stock: %v", err)
	}
	target, err = os.Readlink(active)
	if err != nil || resolveLink(active, target) != stock {
		t.Fatalf("stock link = %q, err=%v", target, err)
	}
	if _, err := os.Stat(loader.paths.custom); !os.IsNotExist(err) {
		t.Fatalf("custom core should be removed after rollback: %v", err)
	}
}

func TestCoreLoaderHashMismatchKeepsStockActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link activation is a Linux runtime behavior")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tampered"))
	}))
	defer server.Close()
	dir := t.TempDir()
	stock := filepath.Join(dir, "xray")
	active := filepath.Join(dir, "rw-core")
	_ = os.WriteFile(stock, []byte("stock"), 0o755)
	_ = os.Symlink(stock, active)
	loader := newCoreLoader(active)
	loader.download = func(ctx context.Context, rawURL, path string, opts artifact.Options) (artifact.Result, error) {
		opts.Client = server.Client()
		return artifact.Download(ctx, rawURL, path, opts)
	}
	if err := loader.prepare(context.Background(), map[string]any{
		"core": map[string]any{"url": server.URL, "sha256": strings.Repeat("0", 64)},
	}); err != nil {
		t.Fatalf("download failure should retain the prior core: %v", err)
	}
	target, _ := os.Readlink(active)
	if resolveLink(active, target) != stock {
		t.Fatalf("hash mismatch changed active core to %q", target)
	}
}
