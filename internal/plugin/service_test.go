package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
)

type fakeFirewall struct {
	mu sync.Mutex

	ready           bool
	owned           bool
	initialize      error
	initializeCalls int
	applyErrors     map[int]error
	commitOnError   map[int]bool
	applyCalls      []firewallConfig
	current         firewallConfig
	applyHook       func(int)
	blockEntered    chan struct{}
	blockCalls      [][]BlockIP
	dynamicBlocks   map[string]struct{}
	unblockCalls    [][]string
	blockErr        error
	unblockErr      error
	closeErrors     map[int]error
	closeCalls      int
}

func (f *fakeFirewall) Initialize(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initializeCalls++
	if f.initialize != nil {
		return f.initialize
	}
	f.owned = true
	f.ready = true
	return nil
}

func (f *fakeFirewall) setInitializeError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initialize = err
}

func (f *fakeFirewall) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeFirewall) Apply(_ context.Context, config firewallConfig) error {
	return f.apply(config, false)
}

func (f *fakeFirewall) Reset(_ context.Context, config firewallConfig) error {
	return f.apply(config, true)
}

func (f *fakeFirewall) apply(config firewallConfig, reset bool) error {
	f.mu.Lock()
	if reset {
		f.owned = true
	}
	config = config.clone()
	f.applyCalls = append(f.applyCalls, config)
	call := len(f.applyCalls)
	err := f.applyErrors[call]
	commitOnError := f.commitOnError[call]
	hook := f.applyHook
	f.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	if err != nil && !commitOnError {
		f.mu.Lock()
		f.ready = false
		f.mu.Unlock()
		return err
	}
	f.mu.Lock()
	f.current = config
	if reset {
		f.dynamicBlocks = make(map[string]struct{})
	}
	f.owned = true
	f.ready = err == nil
	f.mu.Unlock()
	return err
}

func (f *fakeFirewall) BlockIPs(_ context.Context, items []BlockIP) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockCalls = append(f.blockCalls, append([]BlockIP(nil), items...))
	if f.blockErr != nil {
		return f.blockErr
	}
	if f.dynamicBlocks == nil {
		f.dynamicBlocks = make(map[string]struct{})
	}
	for _, item := range items {
		f.dynamicBlocks[item.IP] = struct{}{}
	}
	if f.blockEntered != nil {
		select {
		case f.blockEntered <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeFirewall) hasDynamicBlock(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.dynamicBlocks[ip]
	return ok
}

func (f *fakeFirewall) UnblockIPs(_ context.Context, ips []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unblockCalls = append(f.unblockCalls, append([]string(nil), ips...))
	return f.unblockErr
}

func (f *fakeFirewall) Close(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.owned {
		return nil
	}
	f.closeCalls++
	if err := f.closeErrors[f.closeCalls]; err != nil {
		f.ready = false
		return err
	}
	f.owned = false
	f.ready = false
	return nil
}

func (f *fakeFirewall) failApply(call int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErrors == nil {
		f.applyErrors = make(map[int]error)
	}
	f.applyErrors[call] = err
}

func (f *fakeFirewall) failApplyAfterCommit(call int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.applyErrors == nil {
		f.applyErrors = make(map[int]error)
	}
	if f.commitOnError == nil {
		f.commitOnError = make(map[int]bool)
	}
	f.applyErrors[call] = err
	f.commitOnError[call] = true
}

func (f *fakeFirewall) setApplyHook(hook func(int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyHook = hook
}

func (f *fakeFirewall) snapshot() (calls []firewallConfig, current firewallConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls = make([]firewallConfig, len(f.applyCalls))
	for i := range f.applyCalls {
		calls[i] = f.applyCalls[i].clone()
	}
	return calls, f.current.clone()
}

type mockXray struct {
	removeOutbound int
	removeErr      error
	stopIfOnline   int
	stopErr        error
	events         *[]string
}

type doneObservedContext struct {
	context.Context
	doneObserved chan struct{}
	once         sync.Once
}

type blockingASNResolver struct {
	entered chan struct{}
	release chan struct{}
}

type countingASNResolver struct {
	calls atomic.Int32
}

func (r *countingASNResolver) PrefixesByASN(uint32) (ipv4, ipv6 []string) {
	r.calls.Add(1)
	return []string{"192.0.2.0/24"}, nil
}

func (r blockingASNResolver) PrefixesByASN(uint32) (ipv4, ipv6 []string) {
	close(r.entered)
	<-r.release
	return []string{"192.0.2.0/24"}, nil
}

// Done records that acquireMutation passed its first closing check and entered
// the cancelable operation-gate wait.
func (c *doneObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.doneObserved) })
	return c.Context.Done()
}

func (m *mockXray) StopIfOnline() error {
	m.stopIfOnline++
	if m.events != nil {
		*m.events = append(*m.events, "stop")
	}
	return m.stopErr
}

func (m *mockXray) RemoveTorrentBlockerOutbound() error {
	m.removeOutbound++
	if m.events != nil {
		*m.events = append(*m.events, "remove")
	}
	return m.removeErr
}

func (m *mockXray) resetCalls() {
	m.removeOutbound = 0
	m.stopIfOnline = 0
}

func newReadyService(t *testing.T, state *State, xray XrayController) (*Service, *fakeFirewall) {
	t.Helper()
	backend := &fakeFirewall{}
	service := newServiceWithBackend(state, connections.NewDropper(state.IsWhitelisted), xray, backend)
	if err := service.Initialize(); err != nil {
		t.Fatalf("initialize plugin service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service, backend
}

func TestSyncDisableRemovesTorrentOutboundWhenIncludeTagsAbsent(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*testing.T) *SyncPlugin{
		"disabled section": func(t *testing.T) *SyncPlugin {
			return torrentPlugin(t, false, nil)
		},
		"absent section": func(t *testing.T) *SyncPlugin {
			return filterPlugin(t, "192.0.2.0/24")
		},
	}
	for name, next := range tests {
		t.Run(name, func(t *testing.T) {
			state := NewState()
			xray := &mockXray{}
			service, _ := newReadyService(t, state, xray)
			if response := service.Sync(torrentPlugin(t, true, nil)); !response.Accepted {
				t.Fatal("initial sync was not accepted")
			}
			xray.resetCalls()

			response := service.Sync(next(t))

			if !response.Accepted {
				t.Fatal("sync was not accepted")
			}
			if xray.removeOutbound != 1 || xray.stopIfOnline != 0 {
				t.Fatalf("Xray calls: remove=%d stop=%d, want remove=1 stop=0", xray.removeOutbound, xray.stopIfOnline)
			}
		})
	}
}

func TestSyncDisableWithIncludeTagsRestartsXray(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, _ := newReadyService(t, state, xray)
	service.Sync(torrentPlugin(t, true, []any{"rule-a"}))
	xray.resetCalls()

	response := service.Sync(torrentPlugin(t, false, []any{"rule-a"}))

	if !response.Accepted {
		t.Fatal("sync was not accepted")
	}
	if xray.stopIfOnline != 1 {
		t.Fatalf("xray stop calls = %d, want 1", xray.stopIfOnline)
	}
	if xray.removeOutbound != 0 {
		t.Fatalf("remove outbound calls = %d, want 0", xray.removeOutbound)
	}
}

func TestSyncIncludeRuleTagsChangeRestartsXray(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, _ := newReadyService(t, state, xray)
	service.Sync(torrentPlugin(t, true, []any{"rule-a"}))
	xray.resetCalls()

	service.Sync(torrentPlugin(t, true, []any{"rule-b"}))

	if xray.stopIfOnline != 1 {
		t.Fatalf("StopIfOnline calls = %d, want 1", xray.stopIfOnline)
	}
}

func TestSyncInvalidConfigCleansStateStopsXrayAndPreservesReports(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, _ := newReadyService(t, state, xray)
	service.Sync(mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"connectionDrop": map[string]any{"enabled": true, "whitelistIps": []any{"10.0.0.1"}},
		},
	}))
	state.AddReport(TorrentReport{})
	xray.resetCalls()

	response := service.Sync(mustSyncPlugin(t, map[string]any{
		"uuid":   "00000000-0000-4000-8000-000000000001",
		"name":   "test",
		"config": map[string]any{"sharedLists": "invalid"},
	}))

	if response.Accepted {
		t.Fatal("invalid config was accepted")
	}
	if xray.stopIfOnline != 1 {
		t.Fatalf("StopIfOnline calls = %d, want 1", xray.stopIfOnline)
	}
	if state.HasActivePlugin() {
		t.Fatal("plugin state was not reset")
	}
	if state.ReportsCount() != 1 {
		t.Fatalf("reports count = %d, want 1", state.ReportsCount())
	}
}

func TestSyncUnchangedConfigSkipsAllSideEffects(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	request := torrentPlugin(t, true, nil)
	service.Sync(request)
	xray.resetCalls()
	before, _ := backend.snapshot()

	response := service.Sync(request)
	after, _ := backend.snapshot()

	if !response.Accepted {
		t.Fatal("unchanged config was not accepted")
	}
	if xray.stopIfOnline != 0 || len(after) != len(before) {
		t.Fatalf("unchanged sync caused effects: stop=%d apply=%d->%d", xray.stopIfOnline, len(before), len(after))
	}
}

func TestSyncExactSourceFastPathSkipsASNResolution(t *testing.T) {
	t.Parallel()

	state := NewState()
	resolver := &countingASNResolver{}
	state.SetASNResolver(resolver)
	service, _ := newReadyService(t, state, nil)
	request := mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"sharedLists": []any{
				map[string]any{"name": "ext:test", "type": "asList", "items": []any{float64(64500)}},
			},
			"ingressFilter": map[string]any{"enabled": true, "blockedIps": []any{"ext:test"}},
		},
	})
	if !service.Sync(request).Accepted {
		t.Fatal("initial sync failed")
	}
	firstCalls := resolver.calls.Load()
	if firstCalls == 0 {
		t.Fatal("initial sync did not resolve ASN")
	}
	second := *request
	second.Name = "renamed"
	if !service.Sync(&second).Accepted {
		t.Fatal("identical source sync failed")
	}
	if got := resolver.calls.Load(); got != firstCalls {
		t.Fatalf("exact-source sync repeated ASN lookups: %d -> %d", firstCalls, got)
	}
	if state.currentSnapshot().pluginName != second.Name {
		t.Fatal("fast path did not publish changed identity")
	}
}

func TestSyncValidatesBeforeEquivalentObjectHashShortcut(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, _ := newReadyService(t, state, nil)
	valid := mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"connectionDrop": map[string]any{
				"enabled": true, "whitelistIps": []any{"203.0.113.10"},
			},
		},
	})
	invalid := mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"connectionDrop": map[string]any{
				"enabled": true, "whitelistIps": []any{" 203.0.113.10 "},
			},
		},
	})
	validHash, validErr := hashPluginConfigContext(context.Background(), valid.Config)
	invalidHash, invalidErr := hashPluginConfigContext(context.Background(), invalid.Config)
	if validErr != nil || invalidErr != nil {
		t.Fatalf("hash inputs: valid=%v invalid=%v", validErr, invalidErr)
	}
	if validHash != invalidHash {
		t.Fatal("test inputs do not exercise the trim-equivalent object hash")
	}
	if !service.Sync(valid).Accepted {
		t.Fatal("valid config was not accepted")
	}
	if response := service.Sync(invalid); response.Accepted {
		t.Fatal("hash-equivalent invalid config bypassed validation")
	}
	if state.HasActivePlugin() {
		t.Fatal("invalid config did not clear the active plugin")
	}
}

func TestSyncSameConfigCommitsChangedPluginIdentity(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, _ := newReadyService(t, state, nil)
	first := filterPlugin(t, "192.0.2.0/24")
	if !service.Sync(first).Accepted {
		t.Fatal("initial sync failed")
	}
	second := *first
	second.UUID = "00000000-0000-4000-8000-000000000002"
	second.Name = "replacement"
	if !service.Sync(&second).Accepted {
		t.Fatal("identity update failed")
	}
	snapshot := state.currentSnapshot()
	if snapshot == nil || snapshot.pluginUUID != second.UUID || snapshot.pluginName != second.Name {
		t.Fatalf("plugin identity was not updated: %#v", snapshot)
	}
}

func TestSyncSemanticEquivalentConfigPreservesDynamicFirewallState(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	first := &SyncPlugin{
		UUID:   "00000000-0000-4000-8000-000000000001",
		Name:   "first",
		Config: json.RawMessage(`{"ingressFilter":{"enabled":true,"blockedIps":["192.0.2.1","192.0.2.0/24"]}}`),
	}
	if !service.Sync(first).Accepted {
		t.Fatal("initial sync failed")
	}
	if !service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}}).Accepted {
		t.Fatal("dynamic block failed")
	}
	before, _ := backend.snapshot()
	second := &SyncPlugin{
		UUID:   "00000000-0000-4000-8000-000000000002",
		Name:   "replacement",
		Config: json.RawMessage(`{"ingressFilter":{"blockedIps":["192.0.2.0/24"],"enabled":true}}`),
	}
	if !service.Sync(second).Accepted {
		t.Fatal("semantic equivalent sync failed")
	}
	after, _ := backend.snapshot()
	if len(after) != len(before) {
		t.Fatalf("semantic equivalent sync rebuilt firewall: %d -> %d applies", len(before), len(after))
	}
	if !backend.hasDynamicBlock("203.0.113.10") {
		t.Fatal("semantic equivalent sync discarded the dynamic block")
	}
	snapshot := state.currentSnapshot()
	if snapshot.pluginUUID != second.UUID || snapshot.pluginName != second.Name {
		t.Fatalf("metadata was not published: %#v", snapshot)
	}
}

func TestStaticFilterChangePreservesDynamicTorrentBlock(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(torrentAndFilterPlugin(t, "192.0.2.0/24")).Accepted {
		t.Fatal("initial sync failed")
	}
	if !service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}}).Accepted {
		t.Fatal("dynamic block failed")
	}
	before, _ := backend.snapshot()
	if !service.Sync(torrentAndFilterPlugin(t, "198.51.100.0/24")).Accepted {
		t.Fatal("static filter update failed")
	}
	after, _ := backend.snapshot()
	if len(after) != len(before)+1 {
		t.Fatalf("static update calls = %d -> %d", len(before), len(after))
	}
	if !backend.hasDynamicBlock("203.0.113.10") {
		t.Fatal("static filter update discarded dynamic torrent block")
	}
}

func TestInvalidConfigWithoutSnapshotStillStopsCoreAndResetsFirewall(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	request := mustSyncPlugin(t, map[string]any{
		"uuid":   "00000000-0000-4000-8000-000000000001",
		"name":   "invalid",
		"config": map[string]any{"sharedLists": "invalid"},
	})
	if service.Sync(request).Accepted {
		t.Fatal("invalid config was accepted")
	}
	calls, _ := backend.snapshot()
	if xray.stopIfOnline != 1 || len(calls) != 1 {
		t.Fatalf("invalid cleanup: stop=%d firewall resets=%d", xray.stopIfOnline, len(calls))
	}
}

func TestDisableResetFailurePublishesDegradedStateAndRecovers(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	if !service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}}).Accepted {
		t.Fatal("dynamic block failed")
	}
	before, _ := backend.snapshot()
	backend.failApply(len(before)+1, errors.New("reset failed"))
	if service.Sync(torrentPlugin(t, false, nil)).Accepted {
		t.Fatal("failed reset was accepted")
	}
	after, _ := backend.snapshot()
	if len(after) != len(before)+1 {
		t.Fatalf("failed reset triggered another destructive reset: %d -> %d calls", len(before), len(after))
	}
	if !backend.hasDynamicBlock("203.0.113.10") {
		t.Fatal("failed reset discarded a dynamic block")
	}
	if !state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("failed reset did not publish the desired disabled state as degraded")
	}
	if service.firewallAvailableLocked() {
		t.Fatal("failed reset left the firewall backend available")
	}
	if !service.RecreateTables().Accepted {
		t.Fatal("degraded firewall recovery failed")
	}
	if !service.firewallAvailableLocked() || state.TorrentBlockerEnabled() {
		t.Fatal("recovery did not publish a healthy disabled snapshot")
	}
}

func TestClearResetFailureRetainsRecoverableSnapshot(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, &mockXray{})
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	calls, _ := backend.snapshot()
	backend.failApply(len(calls)+1, errors.New("reset failed"))
	if service.Sync(nil).Accepted {
		t.Fatal("failed clear reset was accepted")
	}
	if !state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("failed clear did not retain a degraded cleanup snapshot")
	}
	if !service.RecreateTables().Accepted || !state.TorrentBlockerEnabled() {
		t.Fatal("failed clear snapshot could not be recovered")
	}
	if !service.Sync(nil).Accepted || state.HasActivePlugin() {
		t.Fatal("clear did not succeed after recovery")
	}
}

func TestSuccessfulResetCommitsSnapshotWhenContextCancelsAtCommitPoint(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, &mockXray{})
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	calls, _ := backend.snapshot()
	ctx, cancel := context.WithCancel(context.Background())
	backend.setApplyHook(func(call int) {
		if call == len(calls)+1 {
			cancel()
		}
	})
	if response := service.SyncContext(ctx, nil); !response.Accepted {
		t.Fatal("committed reset was reported as rejected")
	}
	if state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("successful reset retained the old snapshot after cancellation")
	}
}

func TestClearStopsXrayBeforeRemovingFirewall(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 2)
	xray := &mockXray{events: &events}
	state := NewState()
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	events = events[:0]
	backend.setApplyHook(func(int) { events = append(events, "apply") })
	if !service.Sync(nil).Accepted {
		t.Fatal("clear failed")
	}
	if got := strings.Join(events, ","); got != "stop,apply" {
		t.Fatalf("clear order = %q, want stop,apply", got)
	}
}

func TestClearStopFailureKeepsFirewallAndSnapshot(t *testing.T) {
	t.Parallel()

	xray := &mockXray{}
	state := NewState()
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	xray.stopErr = errors.New("stop failed")
	before, _ := backend.snapshot()
	if service.Sync(nil).Accepted {
		t.Fatal("clear with failed stop was accepted")
	}
	after, _ := backend.snapshot()
	if len(after) != len(before) || !state.HasActivePlugin() {
		t.Fatalf("failed stop changed firewall/state: applies %d -> %d active=%v", len(before), len(after), state.HasActivePlugin())
	}
}

func TestCanceledPlanDoesNotClearActivePlugin(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	oldHash := state.ConfigHash()
	before, _ := backend.snapshot()
	xray.resetCalls()

	resolver := blockingASNResolver{entered: make(chan struct{}), release: make(chan struct{})}
	state.SetASNResolver(resolver)
	request := mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "replacement",
		"config": map[string]any{
			"sharedLists": []any{
				map[string]any{"name": "ext:test", "type": "asList", "items": []any{float64(64500)}},
			},
			"ingressFilter": map[string]any{"enabled": true, "blockedIps": []any{"ext:test"}},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan AcceptedResponse, 1)
	go func() { done <- service.SyncContext(ctx, request) }()
	select {
	case <-resolver.entered:
	case <-time.After(time.Second):
		t.Fatal("plan did not enter ASN resolution")
	}
	cancel()
	close(resolver.release)
	if response := <-done; response.Accepted {
		t.Fatal("canceled plan was accepted")
	}
	after, _ := backend.snapshot()
	if xray.stopIfOnline != 0 || len(after) != len(before) || state.ConfigHash() != oldHash {
		t.Fatalf("canceled plan changed state: stop=%d applies=%d->%d hash=%q", xray.stopIfOnline, len(before), len(after), state.ConfigHash())
	}
}

func TestResetPluginsClearsSnapshotAndPreservesReports(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, _ := newReadyService(t, state, &mockXray{})
	service.Sync(torrentPlugin(t, true, nil))
	state.AddReport(TorrentReport{})

	if err := service.ResetPlugins(); err != nil {
		t.Fatal(err)
	}
	if state.HasActivePlugin() {
		t.Fatal("active plugin was not cleared")
	}
	if state.ReportsCount() != 1 {
		t.Fatalf("reports count = %d, want 1", state.ReportsCount())
	}
}

func TestResetPluginsUsesResetWhileFirewallIsUnhealthy(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(filterPlugin(t, "192.0.2.0/24")).Accepted {
		t.Fatal("initial sync failed")
	}
	backend.mu.Lock()
	backend.ready = false
	before := len(backend.applyCalls)
	backend.mu.Unlock()

	if err := service.ResetPlugins(); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	after := len(backend.applyCalls)
	ready := backend.ready
	backend.mu.Unlock()
	if after != before+1 || !ready {
		t.Fatalf("unhealthy reset calls=%d->%d ready=%v", before, after, ready)
	}
	if state.HasActivePlugin() {
		t.Fatal("successful unhealthy reset retained plugin state")
	}
}

func TestResetPluginsFailureRetainsDegradedSnapshotForRetry(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(filterPlugin(t, "192.0.2.0/24")).Accepted {
		t.Fatal("initial sync failed")
	}
	backend.mu.Lock()
	backend.ready = false
	nextCall := len(backend.applyCalls) + 1
	backend.mu.Unlock()
	backend.failApply(nextCall, errors.New("reset failed"))

	if err := service.ResetPlugins(); err == nil {
		t.Fatal("failed reset returned nil")
	}
	if !state.HasActivePlugin() || state.currentSnapshot().firewallReady {
		t.Fatal("failed reset did not retain a recoverable degraded snapshot")
	}
	if err := service.ResetPlugins(); err != nil {
		t.Fatalf("retry reset: %v", err)
	}
	if state.HasActivePlugin() {
		t.Fatal("successful reset retry retained plugin state")
	}
}

func TestServiceRequiresExplicitInitialization(t *testing.T) {
	t.Parallel()

	state := NewState()
	service := newServiceWithBackend(state, nil, nil, &fakeFirewall{})
	if response := service.Sync(torrentPlugin(t, false, nil)); response.Accepted {
		t.Fatal("sync before initialization was accepted")
	}
	if state.HasActivePlugin() {
		t.Fatal("sync before initialization changed state")
	}
}

func TestUnavailableFirewallAcceptsConfigButDisablesTorrent(t *testing.T) {
	t.Parallel()

	state := NewState()
	backend := &fakeFirewall{initialize: errNFTablesUnavailable}
	xray := &mockXray{}
	service := newServiceWithBackend(state, nil, xray, backend)
	if err := service.Initialize(); !errors.Is(err, errNFTablesUnavailable) {
		t.Fatalf("Initialize error = %v", err)
	}

	response := service.Sync(torrentPlugin(t, true, nil))

	if !response.Accepted || !state.HasActivePlugin() {
		t.Fatalf("degraded sync = %+v active=%v", response, state.HasActivePlugin())
	}
	if state.TorrentBlockerEnabled() {
		t.Fatal("torrent blocker became effective without nftables")
	}
	if xray.stopIfOnline != 0 {
		t.Fatalf("degraded config stopped Xray %d times", xray.stopIfOnline)
	}
}

func TestInitializeRetriesUnavailableFirewall(t *testing.T) {
	t.Parallel()

	backend := &fakeFirewall{initialize: errNFTablesUnavailable}
	service := newServiceWithBackend(NewState(), nil, nil, backend)
	if err := service.Initialize(); !errors.Is(err, errNFTablesUnavailable) {
		t.Fatalf("first Initialize error = %v", err)
	}
	backend.setInitializeError(nil)
	if err := service.Initialize(); err != nil {
		t.Fatalf("retry Initialize: %v", err)
	}
	backend.mu.Lock()
	calls := backend.initializeCalls
	ready := backend.ready
	backend.mu.Unlock()
	if calls != 2 || !ready {
		t.Fatalf("initialize calls=%d ready=%v, want 2/true", calls, ready)
	}
}

func TestCanceledInitializeDoesNotPublishDegradedReadiness(t *testing.T) {
	t.Parallel()

	backend := &fakeFirewall{initialize: context.Canceled}
	service := newServiceWithBackend(NewState(), nil, nil, backend)
	t.Cleanup(func() { _ = service.Close() })
	if err := service.InitializeContext(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("InitializeContext error = %v", err)
	}
	if service.initialized {
		t.Fatal("canceled initialization published initialized state")
	}
	if service.Sync(filterPlugin(t, "192.0.2.0/24")).Accepted {
		t.Fatal("sync was accepted after canceled initialization")
	}
}

func TestCloseCleansOwnedFirewallAfterCanceledInitialize(t *testing.T) {
	t.Parallel()

	backend := &fakeFirewall{initialize: context.Canceled, owned: true}
	service := newServiceWithBackend(NewState(), nil, nil, backend)
	if err := service.InitializeContext(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("InitializeContext error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	closeCalls := backend.closeCalls
	owned := backend.owned
	backend.mu.Unlock()
	if closeCalls != 1 || owned {
		t.Fatalf("close calls=%d owned=%v, want one cleanup", closeCalls, owned)
	}
}

func TestRecreateRecoversDesiredFirewallAndTorrentState(t *testing.T) {
	t.Parallel()

	state := NewState()
	backend := &fakeFirewall{initialize: errNFTablesUnavailable}
	xray := &mockXray{}
	service := newServiceWithBackend(state, nil, xray, backend)
	if err := service.Initialize(); !errors.Is(err, errNFTablesUnavailable) {
		t.Fatalf("Initialize error = %v", err)
	}
	if response := service.Sync(torrentAndFilterPlugin(t, "192.0.2.0/24")); !response.Accepted {
		t.Fatal("degraded sync was not accepted")
	}
	if state.TorrentBlockerEnabled() {
		t.Fatal("torrent blocker became effective before firewall recovery")
	}

	backend.setInitializeError(nil)
	if response := service.RecreateTables(); !response.Accepted {
		t.Fatal("firewall recovery was not accepted")
	}
	if !state.TorrentBlockerEnabled() || xray.stopIfOnline != 1 {
		t.Fatalf("recovered torrent enabled=%v stop calls=%d", state.TorrentBlockerEnabled(), xray.stopIfOnline)
	}
	_, current := backend.snapshot()
	if !reflect.DeepEqual(current.ingressIPs, []string{"192.0.2.0/24"}) {
		t.Fatalf("recovered firewall = %+v", current)
	}
}

func TestRecreateFailureRollsBackToEffectiveDegradedFirewall(t *testing.T) {
	t.Parallel()

	state := NewState()
	backend := &fakeFirewall{initialize: errNFTablesUnavailable}
	xray := &mockXray{stopErr: errors.New("stop failed")}
	service := newServiceWithBackend(state, nil, xray, backend)
	if err := service.Initialize(); !errors.Is(err, errNFTablesUnavailable) {
		t.Fatalf("Initialize error = %v", err)
	}
	if !service.Sync(torrentAndFilterPlugin(t, "192.0.2.0/24")).Accepted {
		t.Fatal("degraded sync was not accepted")
	}

	backend.setInitializeError(nil)
	if response := service.RecreateTables(); response.Accepted {
		t.Fatal("recovery with failed Xray reconciliation was accepted")
	}
	if state.TorrentBlockerEnabled() {
		t.Fatal("failed recovery published effective torrent state")
	}
	_, current := backend.snapshot()
	if len(current.ingressIPs) != 0 || len(current.egressIPs) != 0 || len(current.egressPorts) != 0 {
		t.Fatalf("failed recovery left nft rules active: %+v", current)
	}

	xray.stopErr = nil
	if response := service.RecreateTables(); !response.Accepted {
		t.Fatal("recovery retry was not accepted")
	}
	if !state.TorrentBlockerEnabled() {
		t.Fatal("recovery retry did not publish torrent state")
	}
}

func TestFirewallApplyFailureDoesNotCommitPlan(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, &mockXray{})
	old := filterPlugin(t, "10.0.0.0/8")
	if !service.Sync(old).Accepted {
		t.Fatal("initial sync failed")
	}
	oldHash := state.ConfigHash()
	calls, _ := backend.snapshot()
	backend.failApply(len(calls)+1, errors.New("apply failed"))

	response := service.Sync(filterPlugin(t, "192.0.2.0/24"))

	if response.Accepted {
		t.Fatal("failed firewall plan was accepted")
	}
	if state.ConfigHash() != oldHash || !state.HasActivePlugin() {
		t.Fatal("failed firewall plan replaced committed state")
	}
}

func TestFirewallApplyErrorRestoresPreviousEffectivePlan(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("initial sync failed")
	}
	_, previous := backend.snapshot()
	calls, _ := backend.snapshot()
	backend.failApplyAfterCommit(len(calls)+1, errors.New("reported after commit"))

	if response := service.Sync(filterPlugin(t, "192.0.2.0/24")); response.Accepted {
		t.Fatal("apply error was accepted")
	}
	_, current := backend.snapshot()
	if !reflect.DeepEqual(current, previous) {
		t.Fatalf("firewall after failed apply = %+v, want %+v", current, previous)
	}
}

func TestXrayFailureRollsBackFirewallAndKeepsSnapshot(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	old := filterPlugin(t, "10.0.0.0/8")
	if !service.Sync(old).Accepted {
		t.Fatal("initial sync failed")
	}
	oldHash := state.ConfigHash()
	_, oldFirewall := backend.snapshot()
	xray.stopErr = errors.New("stop failed")

	response := service.Sync(torrentAndFilterPlugin(t, "192.0.2.0/24"))

	if response.Accepted {
		t.Fatal("sync with failed Xray reconciliation was accepted")
	}
	if state.ConfigHash() != oldHash || state.TorrentBlockerEnabled() {
		t.Fatal("failed Xray reconciliation replaced committed state")
	}
	calls, current := backend.snapshot()
	if len(calls) < 3 || !reflect.DeepEqual(current, oldFirewall) {
		t.Fatalf("firewall was not rolled back: calls=%d current=%+v old=%+v", len(calls), current, oldFirewall)
	}
}

func TestDisableRemoveOutboundFailureKeepsFirewallAndTorrentEnabled(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	oldHash := state.ConfigHash()
	_, oldFirewall := backend.snapshot()
	xray.resetCalls()
	xray.removeErr = errors.New("remove outbound failed")

	response := service.Sync(torrentPlugin(t, false, nil))

	if response.Accepted {
		t.Fatal("sync with failed outbound removal was accepted")
	}
	if state.ConfigHash() != oldHash || !state.TorrentBlockerEnabled() {
		t.Fatal("failed outbound removal replaced committed state")
	}
	_, current := backend.snapshot()
	if !reflect.DeepEqual(current, oldFirewall) {
		t.Fatalf("firewall was not rolled back: current=%+v old=%+v", current, oldFirewall)
	}
}

func TestRecreateTablesReplaysCommittedFirewallPlan(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("sync failed")
	}
	_, committed := backend.snapshot()

	if response := service.RecreateTables(); !response.Accepted {
		t.Fatal("recreate was not accepted")
	}
	_, recreated := backend.snapshot()
	if !reflect.DeepEqual(recreated, committed) {
		t.Fatalf("recreated plan = %+v, want %+v", recreated, committed)
	}
}

func TestRecreateHealthyTorrentDoesNotStopXrayBeforeReset(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 2)
	state := NewState()
	xray := &mockXray{events: &events}
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, []any{"rule-a"})).Accepted {
		t.Fatal("initial torrent sync failed")
	}
	xray.resetCalls()
	events = events[:0]
	backend.setApplyHook(func(int) { events = append(events, "reset") })

	if response := service.RecreateTables(); !response.Accepted {
		t.Fatal("recreate was not accepted")
	}
	if got := strings.Join(events, ","); got != "reset" {
		t.Fatalf("recreate events = %q, want reset only", got)
	}
	if xray.stopIfOnline != 0 || xray.removeOutbound != 0 {
		t.Fatalf("healthy recreate called Xray: stop=%d remove=%d", xray.stopIfOnline, xray.removeOutbound)
	}
}

func TestRecreateHealthyTorrentDoesNotDependOnXrayHandlers(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, backend := newReadyService(t, state, xray)
	if !service.Sync(torrentPlugin(t, true, []any{"rule-a"})).Accepted {
		t.Fatal("initial torrent sync failed")
	}
	if !service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}}).Accepted {
		t.Fatal("dynamic block failed")
	}
	xray.resetCalls()
	before, _ := backend.snapshot()
	xray.stopErr = errors.New("stop failed")
	xray.removeErr = errors.New("remove failed")

	if response := service.RecreateTables(); !response.Accepted {
		t.Fatal("healthy recreate was rejected without an Xray transition")
	}
	after, _ := backend.snapshot()
	if len(after) != len(before)+1 || backend.hasDynamicBlock("203.0.113.10") {
		t.Fatalf("recreate did not reset firewall: applies=%d->%d dynamic=%v", len(before), len(after), backend.hasDynamicBlock("203.0.113.10"))
	}
	if !state.TorrentBlockerEnabled() {
		t.Fatal("healthy recreate changed the committed torrent snapshot")
	}
	if xray.stopIfOnline != 0 || xray.removeOutbound != 0 {
		t.Fatalf("healthy recreate called Xray: stop=%d remove=%d", xray.stopIfOnline, xray.removeOutbound)
	}
}

func TestPluginMutationsAreSerialized(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("initial sync failed")
	}

	applyStarted := make(chan struct{})
	releaseApply := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseApply) }) })
	backend.blockEntered = make(chan struct{}, 1)
	backend.setApplyHook(func(call int) {
		if call == 2 {
			close(applyStarted)
			<-releaseApply
		}
	})

	next := filterPlugin(t, "192.0.2.0/24")
	syncDone := make(chan AcceptedResponse, 1)
	go func() { syncDone <- service.Sync(next) }()
	select {
	case <-applyStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sync apply")
	}

	blockAttempted := make(chan struct{})
	blockDone := make(chan AcceptedResponse, 1)
	go func() {
		close(blockAttempted)
		blockDone <- service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}})
	}()
	<-blockAttempted
	select {
	case <-backend.blockEntered:
		t.Fatal("block operation entered backend before sync completed")
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(releaseApply) })
	if response := <-syncDone; !response.Accepted {
		t.Fatal("sync was not accepted")
	}
	if response := <-blockDone; !response.Accepted {
		t.Fatal("block was not accepted")
	}
}

func TestQueuedPluginMutationHonorsRequestCancellation(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	applyStarted := make(chan struct{})
	releaseApply := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseApply) }) })
	backend.setApplyHook(func(call int) {
		if call == 1 {
			close(applyStarted)
			<-releaseApply
		}
	})

	syncDone := make(chan AcceptedResponse, 1)
	go func() { syncDone <- service.Sync(filterPlugin(t, "192.0.2.0/24")) }()
	select {
	case <-applyStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plugin apply")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if response := service.BlockIPsContext(ctx, []BlockIP{{IP: "203.0.113.10", Timeout: 60}}); response.Accepted {
		t.Fatal("canceled queued mutation was accepted")
	}
	backend.mu.Lock()
	blockCalls := len(backend.blockCalls)
	backend.mu.Unlock()
	if blockCalls != 0 {
		t.Fatalf("canceled mutation reached backend %d times", blockCalls)
	}
	releaseOnce.Do(func() { close(releaseApply) })
	if response := <-syncDone; !response.Accepted {
		t.Fatal("sync was not accepted")
	}
}

func TestCloseContextDeadlineIncludesWaitingForOperationGate(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	service.cleanupTimeout = time.Second
	service.operationGate <- struct{}{}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := service.CloseContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Close waited for operation gate for %s", elapsed)
	}
	if !state.HasActivePlugin() || !state.TorrentBlockerEnabled() {
		t.Fatal("timed-out Close discarded state needed for cleanup retry")
	}
	<-service.operationGate

	if !errors.Is(service.Initialize(), errPluginServiceClosed) {
		t.Fatal("initialize after timed-out Close did not return service-closed error")
	}
	if service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("sync was accepted after Close timed out")
	}
	if service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}}).Accepted {
		t.Fatal("block was accepted after Close timed out")
	}
	if !errors.Is(service.ResetPlugins(), errPluginServiceClosed) {
		t.Fatal("reset after timed-out Close did not return service-closed error")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	backend.mu.Lock()
	closeCalls := backend.closeCalls
	backend.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("backend Close calls = %d, want 1", closeCalls)
	}
	if state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("successful Close retry retained plugin state")
	}
}

func TestQueuedMutationIsRejectedWhenCloseStarts(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	service.cleanupTimeout = 30 * time.Millisecond
	service.operationGate <- struct{}{}
	gateReleased := false
	t.Cleanup(func() {
		if !gateReleased {
			<-service.operationGate
		}
	})

	ctx := &doneObservedContext{
		Context:      context.Background(),
		doneObserved: make(chan struct{}),
	}
	mutationDone := make(chan AcceptedResponse, 1)
	go func() {
		mutationDone <- service.BlockIPsContext(ctx, []BlockIP{{IP: "203.0.113.10", Timeout: 60}})
	}()
	select {
	case <-ctx.doneObserved:
	case <-time.After(time.Second):
		t.Fatal("queued mutation did not enter operation admission")
	}

	if err := service.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v", err)
	}
	<-service.operationGate
	gateReleased = true
	select {
	case response := <-mutationDone:
		if response.Accepted {
			t.Fatal("queued mutation was accepted after Close started")
		}
	case <-time.After(time.Second):
		t.Fatal("queued mutation did not return after operation gate was released")
	}
	backend.mu.Lock()
	blockCalls := len(backend.blockCalls)
	backend.mu.Unlock()
	if blockCalls != 0 {
		t.Fatalf("queued mutation reached backend %d times", blockCalls)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
}

func TestCloseWaitsForAdmittedMutationAndRetryCleansItsState(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	service.cleanupTimeout = 30 * time.Millisecond
	applyEntered := make(chan struct{})
	releaseApply := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseApply) }) })
	backend.setApplyHook(func(call int) {
		if call == 1 {
			close(applyEntered)
			<-releaseApply
		}
	})

	mutationDone := make(chan AcceptedResponse, 1)
	go func() {
		mutationDone <- service.Sync(torrentPlugin(t, true, nil))
	}()
	select {
	case <-applyEntered:
	case <-time.After(time.Second):
		t.Fatal("admitted mutation did not reach backend")
	}
	if err := service.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v", err)
	}
	releaseOnce.Do(func() { close(releaseApply) })
	select {
	case response := <-mutationDone:
		if !response.Accepted {
			t.Fatal("admitted mutation was rejected after it began")
		}
	case <-time.After(time.Second):
		t.Fatal("admitted mutation did not finish")
	}
	if !state.HasActivePlugin() || !state.TorrentBlockerEnabled() {
		t.Fatal("admitted mutation did not commit before cleanup retry")
	}
	if service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("new mutation was accepted after Close began")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	if state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("successful Close retry retained admitted mutation state")
	}
}

func TestCloseIsIdempotentAndRejectsLaterMutations(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	closeCalls := backend.closeCalls
	backend.mu.Unlock()
	if closeCalls != 1 {
		t.Fatalf("backend Close calls = %d, want 1", closeCalls)
	}
	if state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("successful Close retained effective plugin state")
	}
	if service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("sync after Close was accepted")
	}
	if service.BlockIPs([]BlockIP{{IP: "203.0.113.10", Timeout: 60}}).Accepted {
		t.Fatal("block after Close was accepted")
	}
	if !errors.Is(service.ResetPlugins(), errPluginServiceClosed) {
		t.Fatal("reset after Close did not return service-closed error")
	}
}

func TestCloseRetriesBackendCleanupAfterFailure(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if !service.Sync(torrentPlugin(t, true, nil)).Accepted {
		t.Fatal("initial sync failed")
	}
	backend.closeErrors = map[int]error{1: errors.New("close failed")}

	if err := service.Close(); err == nil {
		t.Fatal("first Close unexpectedly succeeded")
	}
	if !state.HasActivePlugin() || !state.TorrentBlockerEnabled() {
		t.Fatal("failed Close discarded state needed for cleanup retry")
	}
	if service.Sync(filterPlugin(t, "10.0.0.0/8")).Accepted {
		t.Fatal("mutation was accepted after Close began")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	backend.mu.Lock()
	closeCalls := backend.closeCalls
	backend.mu.Unlock()
	if closeCalls != 2 {
		t.Fatalf("backend Close calls = %d, want 2", closeCalls)
	}
	if state.HasActivePlugin() || state.TorrentBlockerEnabled() {
		t.Fatal("successful Close retry retained plugin state")
	}
}

func torrentPlugin(t *testing.T, enabled bool, includeRuleTags []any) *SyncPlugin {
	return torrentPluginWithDuration(t, enabled, 300, includeRuleTags)
}

func torrentPluginWithDuration(t *testing.T, enabled bool, blockDuration float64, includeRuleTags []any) *SyncPlugin {
	t.Helper()
	torrent := map[string]any{
		"enabled":       enabled,
		"blockDuration": blockDuration,
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

func filterPlugin(t *testing.T, cidr string) *SyncPlugin {
	t.Helper()
	return mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"ingressFilter": map[string]any{"enabled": true, "blockedIps": []any{cidr}},
		},
	})
}

func torrentAndFilterPlugin(t *testing.T, cidr string) *SyncPlugin {
	t.Helper()
	return mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"ingressFilter": map[string]any{"enabled": true, "blockedIps": []any{cidr}},
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": 300,
				"ignoreLists":   map[string]any{},
			},
		},
	})
}
