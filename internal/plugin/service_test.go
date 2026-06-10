package plugin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/plugin"
)

type mockXray struct {
	removeOutbound int
	stopIfOnline   int
}

func (m *mockXray) StopIfOnline() bool {
	m.stopIfOnline++
	return true
}

func (m *mockXray) RemoveTorrentBlockerOutbound() error {
	m.removeOutbound++
	return nil
}

func TestHandleSyncDisableUsesRemoveOutboundWhenNoIncludeTags(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)

	_, _ = state.UpdateFromSync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": 300,
				"ignoreLists":   map[string]any{},
			},
		},
	}))

	body, _ := json.Marshal(map[string]any{
		"plugin": map[string]any{
			"uuid": "00000000-0000-4000-8000-000000000001",
			"name": "test",
			"config": map[string]any{
				"torrentBlocker": map[string]any{
					"enabled":       false,
					"blockDuration": 0,
					"ignoreLists":   map[string]any{},
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	write := func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}

	service.HandleSync(rec, req, write)

	if xray.removeOutbound != 1 {
		t.Fatalf("RemoveTorrentBlockerOutbound calls = %d, want 1", xray.removeOutbound)
	}
	if xray.stopIfOnline != 0 {
		t.Fatalf("StopIfOnline calls = %d, want 0", xray.stopIfOnline)
	}
}

func TestHandleSyncDisableWithStaleIncludeRuleTagsUsesRemoveOutbound(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)

	_, _ = state.UpdateFromSync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":         true,
				"blockDuration":   300,
				"ignoreLists":     map[string]any{},
				"includeRuleTags": []any{"rule-a"},
			},
		},
	}))

	body, _ := json.Marshal(map[string]any{
		"plugin": map[string]any{
			"uuid": "00000000-0000-4000-8000-000000000001",
			"name": "test",
			"config": map[string]any{
				"torrentBlocker": map[string]any{
					"enabled":         false,
					"blockDuration":   0,
					"ignoreLists":     map[string]any{},
					"includeRuleTags": []any{"rule-a"},
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	write := func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}

	service.HandleSync(rec, req, write)

	if xray.removeOutbound != 1 {
		t.Fatalf("RemoveTorrentBlockerOutbound calls = %d, want 1", xray.removeOutbound)
	}
	if xray.stopIfOnline != 0 {
		t.Fatalf("StopIfOnline calls = %d, want 0", xray.stopIfOnline)
	}
}

func TestHandleSyncIncludeRuleTagsChangeRestartsXray(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)

	_, _ = state.UpdateFromSync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":         true,
				"blockDuration":   300,
				"ignoreLists":     map[string]any{},
				"includeRuleTags": []any{"rule-a"},
			},
		},
	}))

	body, _ := json.Marshal(map[string]any{
		"plugin": map[string]any{
			"uuid": "00000000-0000-4000-8000-000000000001",
			"name": "test",
			"config": map[string]any{
				"torrentBlocker": map[string]any{
					"enabled":         true,
					"blockDuration":   300,
					"ignoreLists":     map[string]any{},
					"includeRuleTags": []any{"rule-b"},
				},
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	write := func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}

	service.HandleSync(rec, req, write)

	if xray.stopIfOnline != 1 {
		t.Fatalf("StopIfOnline calls = %d, want 1", xray.stopIfOnline)
	}
}

func TestHandleSyncInvalidConfigStopsXray(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)

	body, _ := json.Marshal(map[string]any{
		"plugin": map[string]any{
			"uuid": "00000000-0000-4000-8000-000000000001",
			"name": "test",
			"config": map[string]any{
				"sharedLists": "invalid",
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	write := func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}

	service.HandleSync(rec, req, write)

	if xray.stopIfOnline != 1 {
		t.Fatalf("StopIfOnline calls = %d, want 1", xray.stopIfOnline)
	}
	if state.HasActivePlugin() {
		t.Fatal("expected plugin state reset after invalid config")
	}
}

func TestHandleSyncUnchangedConfigSkipsRestart(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)

	pluginConfig := map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": 300,
				"ignoreLists":   map[string]any{},
			},
		},
	}
	_, _ = state.UpdateFromSync(mustSyncPlugin(t, pluginConfig))

	body, _ := json.Marshal(map[string]any{"plugin": pluginConfig})
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	write := func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}

	service.HandleSync(rec, req, write)

	if xray.stopIfOnline != 0 || xray.removeOutbound != 0 {
		t.Fatalf("expected no xray side effects, stop=%d remove=%d", xray.stopIfOnline, xray.removeOutbound)
	}
}

func TestResetPluginsClearsActivePlugin(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), &mockXray{})

	_, _ = state.UpdateFromSync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": 300,
				"ignoreLists":   map[string]any{},
			},
		},
	}))
	if !state.HasActivePlugin() {
		t.Fatal("expected active plugin before reset")
	}

	service.ResetPlugins()

	if state.HasActivePlugin() {
		t.Fatal("expected plugin state cleared after ResetPlugins")
	}
}
