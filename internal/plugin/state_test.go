package plugin

import (
	"encoding/json"
	"testing"
)

func mustSyncPlugin(t *testing.T, raw map[string]any) *SyncPlugin {
	t.Helper()
	payload, err := NewSyncPluginFromEnvelope(raw)
	if err != nil {
		t.Fatalf("sync plugin: %v", err)
	}
	return payload
}

func TestSyncNullWithoutActiveIsRejected(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, _ := newReadyService(t, state, nil)
	if response := service.Sync(nil); response.Accepted {
		t.Fatal("empty sync without active plugin was accepted")
	}
}

func TestSyncCommitsWhitelist(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, _ := newReadyService(t, state, nil)
	response := service.Sync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"connectionDrop": map[string]any{
				"enabled":      true,
				"whitelistIps": []any{"10.0.0.1"},
			},
		},
	}))
	if !response.Accepted {
		t.Fatal("sync was not accepted")
	}
	if !state.IsWhitelisted("10.0.0.1") || state.IsWhitelisted("10.0.0.2") {
		t.Fatal("committed whitelist does not match the plan")
	}
}

func TestSyncResolvesSharedWhitelist(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, _ := newReadyService(t, state, nil)
	response := service.Sync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"sharedLists": []any{
				map[string]any{
					"type":  "ipList",
					"name":  "ext:trusted",
					"items": []any{"10.0.0.5"},
				},
			},
			"connectionDrop": map[string]any{
				"enabled":      true,
				"whitelistIps": []any{"ext:trusted"},
			},
		},
	}))
	if !response.Accepted || !state.IsWhitelisted("10.0.0.5") {
		t.Fatalf("shared whitelist was not committed: response=%+v", response)
	}
}

func TestNewSyncPluginFromEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"uuid":   "id",
		"name":   "n",
		"config": map[string]any{"a": float64(1)},
	}
	payload, err := NewSyncPluginFromEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(payload.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["a"].(float64) != 1 {
		t.Fatalf("config = %#v", cfg)
	}
}
