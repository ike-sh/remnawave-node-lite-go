package xray

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type preStartProvider struct {
	enabled bool
	files   []string
}

func (p preStartProvider) TorrentBlockerEnabled() bool              { return false }
func (p preStartProvider) TorrentBlockerIncludeRuleTags() []string  { return nil }
func (p preStartProvider) PreStartCleanupSockets() (bool, []string) { return p.enabled, p.files }

func TestPreStartNeverRemovesRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keep.sock")
	if err := os.WriteFile(path, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(Options{XrayBin: "missing", GeoDir: t.TempDir(), LogDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager.SetTorrentBlockerProvider(preStartProvider{enabled: true, files: []string{path}})
	manager.runPreStart()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("regular file was removed: %v", err)
	}
}

func TestResolveSocketPatternCapsMatches(t *testing.T) {
	dir := t.TempDir()
	for index := 0; index < maxPreStartMatches+20; index++ {
		path := filepath.Join(dir, "socket-"+formatIndex(index)+".sock")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	matches := resolveSocketPattern(filepath.Join(dir, "*.sock"))
	if len(matches) != maxPreStartMatches {
		t.Fatalf("matches = %d, want %d", len(matches), maxPreStartMatches)
	}
}

func formatIndex(index int) string {
	return fmt.Sprintf("%03d", index)
}
