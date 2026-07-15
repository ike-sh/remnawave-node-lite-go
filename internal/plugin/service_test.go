package plugin

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
)

type fakeFirewall struct {
	mu sync.Mutex

	ready        bool
	initialize   error
	applyErrors  map[int]error
	applyCalls   []firewallConfig
	current      firewallConfig
	applyHook    func(int)
	blockEntered chan struct{}
	blockCalls   [][]BlockIP
	unblockCalls [][]string
	closeCalls   int
}

func (f *fakeFirewall) Initialize() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.initialize != nil {
		return f.initialize
	}
	f.ready = true
	return nil
}

func (f *fakeFirewall) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *fakeFirewall) Apply(config firewallConfig) error {
	f.mu.Lock()
	config = config.clone()
	f.applyCalls = append(f.applyCalls, config)
	call := len(f.applyCalls)
	err := f.applyErrors[call]
	hook := f.applyHook
	f.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.current = config
	f.mu.Unlock()
	return nil
}

func (f *fakeFirewall) BlockIPs(items []BlockIP) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockCalls = append(f.blockCalls, append([]BlockIP(nil), items...))
	if f.blockEntered != nil {
		select {
		case f.blockEntered <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeFirewall) UnblockIPs(ips []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unblockCalls = append(f.unblockCalls, append([]string(nil), ips...))
	return nil
}

func (f *fakeFirewall) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
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
	stopIfOnline   int
	removeErr      error
	stopErr        error
}

func (m *mockXray) StopIfOnline() error {
	m.stopIfOnline++
	return m.stopErr
}

func (m *mockXray) RemoveTorrentBlockerOutbound() error {
	m.removeOutbound++
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
	return service, backend
}

func TestSyncDisableUsesRemoveOutboundWhenIncludeTagsAbsent(t *testing.T) {
	t.Parallel()

	state := NewState()
	xray := &mockXray{}
	service, _ := newReadyService(t, state, xray)
	if response := service.Sync(torrentPlugin(t, true, nil)); !response.Accepted {
		t.Fatal("initial sync was not accepted")
	}
	xray.resetCalls()

	response := service.Sync(torrentPlugin(t, false, nil))

	if !response.Accepted {
		t.Fatal("sync was not accepted")
	}
	if xray.removeOutbound != 1 || xray.stopIfOnline != 0 {
		t.Fatalf("xray calls: remove=%d stop=%d", xray.removeOutbound, xray.stopIfOnline)
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
	if xray.stopIfOnline != 1 || xray.removeOutbound != 0 {
		t.Fatalf("xray calls: stop=%d remove=%d", xray.stopIfOnline, xray.removeOutbound)
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
	if xray.stopIfOnline != 0 || xray.removeOutbound != 0 || len(after) != len(before) {
		t.Fatalf("unchanged sync caused effects: stop=%d remove=%d apply=%d->%d", xray.stopIfOnline, xray.removeOutbound, len(before), len(after))
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

func TestRemoveOutboundFailureRollsBackFirewallAndKeepsTorrentEnabled(t *testing.T) {
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
	xray.removeErr = errors.New("remove failed")

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

func TestCloseIsIdempotentAndRejectsLaterMutations(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
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

func torrentPlugin(t *testing.T, enabled bool, includeRuleTags []any) *SyncPlugin {
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
