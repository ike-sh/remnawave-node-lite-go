package nodehandler_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodehandler"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

type connectionCleanupProvider struct {
	stubProvider
	mu          sync.Mutex
	entries     map[string][]xtls.IPEntry
	resetFlags  []bool
	lookupErr   error
	resetErr    error
	removeErr   map[string]string
	removeCalls atomic.Int64
}

func (p *connectionCleanupProvider) GetUserIPList(_ context.Context, userID string, reset bool) ([]xtls.IPEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resetFlags = append(p.resetFlags, reset)
	if !reset && p.lookupErr != nil {
		return nil, p.lookupErr
	}
	if reset && p.resetErr != nil {
		return nil, p.resetErr
	}
	entries := append([]xtls.IPEntry(nil), p.entries[userID]...)
	if reset {
		delete(p.entries, userID)
	}
	return entries, nil
}

func (p *connectionCleanupProvider) HandlerRemoveUser(_ context.Context, tag, _ string) xtls.HandlerResult {
	p.removeCalls.Add(1)
	if message := p.removeErr[tag]; message != "" {
		return xtls.HandlerResult{OK: false, Message: message}
	}
	return xtls.HandlerResult{OK: true}
}

type stagedConnectionDropper struct {
	available bool
	failures  atomic.Int64
	calls     atomic.Int64
}

func (d *stagedConnectionDropper) Available() bool { return d.available }

func (d *stagedConnectionDropper) DropIPs(context.Context, []string) bool {
	d.calls.Add(1)
	return d.failures.Add(-1) < 0
}

func (d *stagedConnectionDropper) DropUsers(context.Context, connections.IPListProvider, []string) bool {
	panic("unexpected DropUsers call")
}

type recordingConnectionDropper struct {
	available bool
	mu        sync.Mutex
	batches   [][]string
}

func (d *recordingConnectionDropper) Available() bool { return d.available }

func (d *recordingConnectionDropper) DropIPs(_ context.Context, ips []string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.batches = append(d.batches, append([]string(nil), ips...))
	return true
}

func (d *recordingConnectionDropper) DropUsers(context.Context, connections.IPListProvider, []string) bool {
	panic("unexpected DropUsers call")
}

func TestRemoveUsersRetriesSocketKillWithoutResettingOnlineStats(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1"}},
		entries: map[string][]xtls.IPEntry{
			"u1": {{IP: "203.0.113.10"}},
		},
	}
	dropper := &stagedConnectionDropper{available: true}
	dropper.failures.Store(1)
	service := nodehandler.NewService(provider, dropper)
	request := nodehandler.RemoveUsersRequest{Users: []nodehandler.RemoveUsersItem{{UserID: "u1", HashUUID: "hash-1"}}}

	response, err := service.RemoveUsers(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error == nil || *response.Error != "failed to drop user connections" {
		t.Fatalf("first response = %#v", response)
	}
	provider.mu.Lock()
	firstFlags := append([]bool(nil), provider.resetFlags...)
	remaining := append([]xtls.IPEntry(nil), provider.entries["u1"]...)
	provider.mu.Unlock()
	if !slices.Equal(firstFlags, []bool{false}) {
		t.Fatalf("first reset flags = %v, want [false]", firstFlags)
	}
	if len(remaining) != 1 {
		t.Fatalf("failed socket kill cleared IP stats: %v", remaining)
	}

	response, err = service.RemoveUsers(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Error != nil {
		t.Fatalf("retry response = %#v", response)
	}
	provider.mu.Lock()
	allFlags := append([]bool(nil), provider.resetFlags...)
	remaining = append([]xtls.IPEntry(nil), provider.entries["u1"]...)
	provider.mu.Unlock()
	if !slices.Equal(allFlags, []bool{false, false}) {
		t.Fatalf("all reset flags = %v, want read-only lookups", allFlags)
	}
	if len(remaining) != 1 {
		t.Fatalf("connection cleanup destructively reset online stats: %v", remaining)
	}
	if got := dropper.calls.Load(); got != 2 {
		t.Fatalf("socket drop calls = %d, want 2", got)
	}
}

func TestRemoveUsersBatchesConnectionDropAfterReadOnlyStatsLookups(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1"}},
		entries: map[string][]xtls.IPEntry{
			"u1": {{IP: "203.0.113.10"}},
			"u2": {{IP: "198.51.100.20"}},
		},
	}
	dropper := &recordingConnectionDropper{available: true}
	service := nodehandler.NewService(provider, dropper)

	response, err := service.RemoveUsers(context.Background(), nodehandler.RemoveUsersRequest{Users: []nodehandler.RemoveUsersItem{
		{UserID: "u1", HashUUID: "hash-1"},
		{UserID: "u2", HashUUID: "hash-2"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
	dropper.mu.Lock()
	batches := append([][]string(nil), dropper.batches...)
	dropper.mu.Unlock()
	if len(batches) != 1 || !slices.Equal(batches[0], []string{"203.0.113.10", "198.51.100.20"}) {
		t.Fatalf("drop batches = %v, want one aggregated batch", batches)
	}
	provider.mu.Lock()
	flags := append([]bool(nil), provider.resetFlags...)
	provider.mu.Unlock()
	if !slices.Equal(flags, []bool{false, false}) {
		t.Fatalf("IP stats calls = %v, want read-only lookups", flags)
	}
}

func TestRemoveUserNeverRequestsDestructiveOnlineStatsReset(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1"}},
		entries: map[string][]xtls.IPEntry{
			"u1": {{IP: "203.0.113.10"}},
		},
		resetErr: errors.New("reset unavailable"),
	}
	dropper := &stagedConnectionDropper{available: true}
	service := nodehandler.NewService(provider, dropper)
	request := nodehandler.RemoveUserRequest{Username: "u1", VlessUUID: "hash-1"}

	response, err := service.RemoveUser(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Error != nil {
		t.Fatalf("read-only cleanup response = %#v", response)
	}
	provider.mu.Lock()
	remaining := append([]xtls.IPEntry(nil), provider.entries["u1"]...)
	flags := append([]bool(nil), provider.resetFlags...)
	provider.mu.Unlock()
	if len(remaining) != 1 {
		t.Fatalf("connection cleanup destructively reset online stats: %v", remaining)
	}
	if !slices.Equal(flags, []bool{false}) {
		t.Fatalf("IP stats calls = %v, want one read-only lookup", flags)
	}
}

func TestRemoveUsersSkipsIPStatsWithoutNetAdminCapability(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1"}},
		entries: map[string][]xtls.IPEntry{
			"u1": {{IP: "203.0.113.10"}},
		},
	}
	dropper := &stagedConnectionDropper{available: false}
	service := nodehandler.NewService(provider, dropper)
	response, err := service.RemoveUsers(context.Background(), nodehandler.RemoveUsersRequest{
		Users: []nodehandler.RemoveUsersItem{{UserID: "u1", HashUUID: "hash-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Error != nil {
		t.Fatalf("response = %#v", response)
	}
	provider.mu.Lock()
	flags := append([]bool(nil), provider.resetFlags...)
	provider.mu.Unlock()
	if len(flags) != 0 {
		t.Fatalf("IP stats calls without CAP_NET_ADMIN = %v", flags)
	}
	if got := dropper.calls.Load(); got != 0 {
		t.Fatalf("socket drop calls without CAP_NET_ADMIN = %d", got)
	}
	if got := provider.removeCalls.Load(); got != 1 {
		t.Fatalf("handler remove calls = %d, want 1", got)
	}
}

func TestRemoveUsersReportsIPLookupFailureWithoutReset(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1"}},
		lookupErr:    errors.New("stats RPC unavailable"),
	}
	dropper := &stagedConnectionDropper{available: true}
	service := nodehandler.NewService(provider, dropper)
	response, err := service.RemoveUsers(context.Background(), nodehandler.RemoveUsersRequest{
		Users: []nodehandler.RemoveUsersItem{{UserID: "u1", HashUUID: "hash-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error == nil || *response.Error != "failed to read user IP stats" {
		t.Fatalf("response = %#v", response)
	}
	provider.mu.Lock()
	flags := append([]bool(nil), provider.resetFlags...)
	provider.mu.Unlock()
	if !slices.Equal(flags, []bool{false}) {
		t.Fatalf("IP stats calls = %v, want one non-reset lookup", flags)
	}
	if got := dropper.calls.Load(); got != 0 {
		t.Fatalf("socket drop calls after lookup failure = %d", got)
	}
	if got := provider.removeCalls.Load(); got != 0 {
		t.Fatalf("handler remove calls after lookup failure = %d, want 0", got)
	}
}

func TestRemoveUserStopsBeforeMutationWhenIPLookupFails(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1", "in-2"}},
		lookupErr:    errors.New("stats RPC unavailable"),
	}
	dropper := &stagedConnectionDropper{available: true}
	service := nodehandler.NewService(provider, dropper)

	response, err := service.RemoveUser(context.Background(), nodehandler.RemoveUserRequest{
		Username:  "u1",
		VlessUUID: "hash-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error == nil || *response.Error != "failed to read user IP stats" {
		t.Fatalf("response = %#v", response)
	}
	if got := provider.removeCalls.Load(); got != 0 {
		t.Fatalf("handler remove calls after lookup failure = %d, want 0", got)
	}
	if got := dropper.calls.Load(); got != 0 {
		t.Fatalf("socket drop calls after lookup failure = %d, want 0", got)
	}
}

func TestRemoveUserPreservesIPStatsWhenAnInboundRemovalFails(t *testing.T) {
	t.Parallel()

	provider := &connectionCleanupProvider{
		stubProvider: stubProvider{inboundTags: []string{"in-1", "in-2"}},
		entries: map[string][]xtls.IPEntry{
			"u1": {{IP: "203.0.113.10"}},
		},
		removeErr: map[string]string{"in-2": "remove unavailable"},
	}
	dropper := &stagedConnectionDropper{available: true}
	service := nodehandler.NewService(provider, dropper)

	response, err := service.RemoveUser(context.Background(), nodehandler.RemoveUserRequest{
		Username:  "u1",
		VlessUUID: "hash-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error == nil || *response.Error != "remove unavailable" {
		t.Fatalf("response = %#v", response)
	}
	provider.mu.Lock()
	flags := append([]bool(nil), provider.resetFlags...)
	remaining := append([]xtls.IPEntry(nil), provider.entries["u1"]...)
	provider.mu.Unlock()
	if !slices.Equal(flags, []bool{false}) {
		t.Fatalf("IP stats calls = %v, want one non-reset lookup", flags)
	}
	if len(remaining) != 1 {
		t.Fatalf("failed inbound removal cleared IP stats: %v", remaining)
	}
	if got := provider.removeCalls.Load(); got != 2 {
		t.Fatalf("handler remove calls = %d, want 2", got)
	}
	if got := dropper.calls.Load(); got != 0 {
		t.Fatalf("socket drop calls after inbound removal failure = %d, want 0", got)
	}
}
