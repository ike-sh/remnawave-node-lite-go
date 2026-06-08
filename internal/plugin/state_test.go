package plugin_test

import (
	"testing"

	"remnawave-node-lite-go/internal/plugin"
)

func TestUpdateFromSyncNullWithoutActive(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	changed, accepted := state.UpdateFromSync(nil)
	if changed || accepted {
		t.Fatalf("expected no-op, got changed=%v accepted=%v", changed, accepted)
	}
}

func TestUpdateFromSyncStoresWhitelist(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	_, accepted := state.UpdateFromSync(map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"connectionDrop": map[string]any{
				"enabled":      true,
				"whitelistIps": []any{"10.0.0.1"},
			},
		},
	})
	if !accepted {
		t.Fatal("expected accepted sync")
	}
	if state.IsWhitelisted("10.0.0.1") != true {
		t.Fatal("expected whitelisted ip")
	}
	if state.IsWhitelisted("10.0.0.2") {
		t.Fatal("unexpected whitelist match")
	}
}

func TestUpdateFromSyncResolvesSharedWhitelist(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	_, accepted := state.UpdateFromSync(map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"sharedLists": []any{
				map[string]any{
					"type":  "ipList",
					"name":  "trusted",
					"items": []any{"10.0.0.5"},
				},
			},
			"connectionDrop": map[string]any{
				"enabled":      true,
				"whitelistIps": []any{"ext:trusted"},
			},
		},
	})
	if !accepted {
		t.Fatal("expected accepted sync")
	}
	if !state.IsWhitelisted("10.0.0.5") {
		t.Fatal("expected shared list ip to be whitelisted")
	}
}

func TestUpdateFromSyncSkipsUnchangedHash(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	pluginConfig := map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{"enabled": true},
		},
	}
	changed1, _ := state.UpdateFromSync(pluginConfig)
	changed2, accepted2 := state.UpdateFromSync(pluginConfig)
	if !changed1 {
		t.Fatal("first sync should mark changed")
	}
	if changed2 || !accepted2 {
		t.Fatalf("second sync should be unchanged accepted, got changed=%v accepted=%v", changed2, accepted2)
	}
}
