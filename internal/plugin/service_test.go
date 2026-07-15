package plugin_test

import (
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
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

func TestSyncDisableUsesRemoveOutboundWhenNoIncludeTags(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)
	_, _ = state.UpdateFromSync(torrentPlugin(t, true, nil))

	response := service.Sync(torrentPlugin(t, false, nil))

	if !response.Accepted {
		t.Fatal("sync was not accepted")
	}
	if xray.removeOutbound != 1 {
		t.Fatalf("RemoveTorrentBlockerOutbound calls = %d, want 1", xray.removeOutbound)
	}
	if xray.stopIfOnline != 0 {
		t.Fatalf("StopIfOnline calls = %d, want 0", xray.stopIfOnline)
	}
}

func TestSyncDisableWithStaleIncludeRuleTagsUsesRemoveOutbound(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)
	_, _ = state.UpdateFromSync(torrentPlugin(t, true, []any{"rule-a"}))

	service.Sync(torrentPlugin(t, false, []any{"rule-a"}))

	if xray.removeOutbound != 1 {
		t.Fatalf("RemoveTorrentBlockerOutbound calls = %d, want 1", xray.removeOutbound)
	}
	if xray.stopIfOnline != 0 {
		t.Fatalf("StopIfOnline calls = %d, want 0", xray.stopIfOnline)
	}
}

func TestSyncIncludeRuleTagsChangeRestartsXray(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)
	_, _ = state.UpdateFromSync(torrentPlugin(t, true, []any{"rule-a"}))

	service.Sync(torrentPlugin(t, true, []any{"rule-b"}))

	if xray.stopIfOnline != 1 {
		t.Fatalf("StopIfOnline calls = %d, want 1", xray.stopIfOnline)
	}
}

func TestSyncInvalidConfigStopsXray(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)
	request := mustSyncPlugin(t, map[string]any{
		"uuid":   "00000000-0000-4000-8000-000000000001",
		"name":   "test",
		"config": map[string]any{"sharedLists": "invalid"},
	})

	response := service.Sync(request)

	if response.Accepted {
		t.Fatal("invalid config was accepted")
	}
	if xray.stopIfOnline != 1 {
		t.Fatalf("StopIfOnline calls = %d, want 1", xray.stopIfOnline)
	}
	if state.HasActivePlugin() {
		t.Fatal("expected plugin state reset after invalid config")
	}
}

func TestSyncUnchangedConfigSkipsRestart(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	xray := &mockXray{}
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), xray)
	request := torrentPlugin(t, true, nil)
	_, _ = state.UpdateFromSync(request)

	response := service.Sync(request)

	if !response.Accepted {
		t.Fatal("unchanged config was not accepted")
	}
	if xray.stopIfOnline != 0 || xray.removeOutbound != 0 {
		t.Fatalf("expected no xray side effects, stop=%d remove=%d", xray.stopIfOnline, xray.removeOutbound)
	}
}

func TestResetPluginsClearsActivePlugin(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), &mockXray{})
	_, _ = state.UpdateFromSync(torrentPlugin(t, true, nil))
	if !state.HasActivePlugin() {
		t.Fatal("expected active plugin before reset")
	}

	service.ResetPlugins()

	if state.HasActivePlugin() {
		t.Fatal("expected plugin state cleared after ResetPlugins")
	}
}

func torrentPlugin(t *testing.T, enabled bool, includeRuleTags []any) *plugin.SyncPlugin {
	t.Helper()
	torrent := map[string]any{
		"enabled":       enabled,
		"blockDuration": 300,
		"ignoreLists":   map[string]any{},
	}
	if includeRuleTags != nil {
		torrent["includeRuleTags"] = includeRuleTags
	}
	return mustSyncPlugin(t, map[string]any{
		"uuid":   "00000000-0000-4000-8000-000000000001",
		"name":   "test",
		"config": map[string]any{"torrentBlocker": torrent},
	})
}
