//go:build unix

package xray

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPreStartRemovesUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	unixListener := listener.(*net.UnixListener)
	unixListener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Options{XrayBin: "missing", GeoDir: t.TempDir(), LogDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetTorrentBlockerProvider(preStartProvider{enabled: true, files: []string{path}})
	response := manager.Start(context.Background(), StartRequest{XrayConfig: map[string]any{}})
	if response.IsStarted {
		t.Fatal("missing test core should not start")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("stale socket still exists: %v", err)
	}
}
