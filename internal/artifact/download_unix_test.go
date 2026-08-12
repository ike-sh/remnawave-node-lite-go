//go:build unix

package artifact

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadDoesNotFollowTemporarySymlink(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("download"))
	}))
	defer server.Close()

	dir := t.TempDir()
	destination := filepath.Join(dir, "artifact")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, destination+".download"); err != nil {
		t.Fatal(err)
	}

	if _, err := Download(context.Background(), server.URL, destination, Options{Client: server.Client()}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "keep" {
		t.Fatalf("temporary symlink target changed: %q, err=%v", got, err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "download" {
		t.Fatalf("downloaded artifact = %q, err=%v", got, err)
	}
}
