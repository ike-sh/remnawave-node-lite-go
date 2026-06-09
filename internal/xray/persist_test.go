package xray

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistStartRoundTrip(t *testing.T) {
	dir := t.TempDir()
	req := StartRequest{
		Internals: StartInternals{
			Hashes: ConfigHash{
				EmptyConfig: "abc",
				Inbounds: []InboundHash{
					{Tag: "in-1", Hash: "h1", UsersCount: 1},
				},
			},
		},
		XrayConfig: map[string]any{"log": map[string]any{"loglevel": "warning"}},
	}

	if err := savePersistedStart(dir, req); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := loadPersistedStart(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded request")
	}
	if loaded.Internals.Hashes.EmptyConfig != "abc" {
		t.Fatalf("unexpected hash: %#v", loaded.Internals.Hashes)
	}
	if _, ok := loaded.XrayConfig["log"]; !ok {
		t.Fatalf("expected xrayConfig preserved: %#v", loaded.XrayConfig)
	}

	if err := clearPersistedStart(dir); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, persistedStartFile)); !os.IsNotExist(err) {
		t.Fatal("expected persisted file removed")
	}
}
