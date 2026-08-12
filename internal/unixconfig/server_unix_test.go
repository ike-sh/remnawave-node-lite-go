//go:build unix

package unixconfig

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveUnixSocketRemovesSocket(t *testing.T) {
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
	if err := removeUnixSocket(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket was not removed: %v", err)
	}
}
