package connections

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

func testDropper(available bool, localIPs ...string) (*Dropper, *[]string) {
	locals := make(map[netip.Addr]struct{}, len(localIPs))
	for _, ip := range localIPs {
		locals[netip.MustParseAddr(ip).Unmap()] = struct{}{}
	}
	var mu sync.Mutex
	killed := make([]string, 0)
	dropper := &Dropper{
		available:     available,
		isWhitelisted: func(string) bool { return false },
		localIPs:      locals,
		localIPsReady: true,
		killSockets: func(_ context.Context, targets []netip.Addr) error {
			mu.Lock()
			for _, target := range targets {
				killed = append(killed, target.String())
			}
			mu.Unlock()
			return nil
		},
	}
	return dropper, &killed
}

func TestDropIPsRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"not-an-ip",
		"0.0.0.0",
		"::",
		"127.0.0.1",
		"::ffff:127.0.0.1",
		"169.254.10.1",
		"fe80::1",
		"fe80::1%eth0",
		"224.0.0.1",
		"ff02::1",
		"255.255.255.255",
		"198.51.100.9",
	}
	for _, ip := range tests {
		ip := ip
		t.Run(ip, func(t *testing.T) {
			t.Parallel()
			dropper, killed := testDropper(true, "198.51.100.9")
			if dropper.DropIPs(context.Background(), []string{ip}) {
				t.Fatalf("unsafe target %q reported success", ip)
			}
			if len(*killed) != 0 {
				t.Fatalf("unsafe target %q reached socket killer: %v", ip, *killed)
			}
		})
	}
}

func TestDropIPsAggregatesUnsafeTargetsAndProcessesOneValidBatch(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	inputs := make([]string, 0, 1_025)
	for range 512 {
		inputs = append(inputs, "not-an-ip", "127.0.0.1")
	}
	inputs = append(inputs, "203.0.113.10")
	if dropper.DropIPs(context.Background(), inputs) {
		t.Fatal("batch containing unsafe targets reported success")
	}
	if !slices.Equal(*killed, []string{"203.0.113.10"}) {
		t.Fatalf("socket kill targets = %v, want only the valid target", *killed)
	}
}

func TestDropIPsCanonicalizesDeduplicatesAndReportsFailure(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	var calls atomic.Int64
	dropper.killSockets = func(_ context.Context, targets []netip.Addr) error {
		calls.Add(1)
		for _, target := range targets {
			*killed = append(*killed, target.String())
			if target.String() == "2001:db8::1" {
				return errors.New("socket destroy failed")
			}
		}
		return nil
	}

	if dropper.DropIPs(context.Background(), []string{
		"203.0.113.10",
		"203.0.113.10",
		"2001:0db8::1",
	}) {
		t.Fatal("one failed socket kill must make the result false")
	}
	if !slices.Equal(*killed, []string{"203.0.113.10", "2001:db8::1"}) {
		t.Fatalf("killed = %v, want canonical unique targets", *killed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("socket killer calls = %d, want one aggregated batch", got)
	}
}

func TestDropIPsHonorsWhitelistBeforeCapabilityCheck(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(false)
	dropper.isWhitelisted = func(ip string) bool { return ip == "203.0.113.10" }
	if !dropper.DropIPs(context.Background(), []string{"203.0.113.10"}) {
		t.Fatal("an intentionally whitelisted target should be a successful no-op")
	}
	if len(*killed) != 0 {
		t.Fatalf("whitelisted target reached socket killer: %v", *killed)
	}
	if dropper.DropIPs(context.Background(), []string{"203.0.113.11"}) {
		t.Fatal("missing capability must be reported for a killable target")
	}
}

func TestDropIPsPropagatesCanceledContext(t *testing.T) {
	t.Parallel()

	dropper, _ := testDropper(true)
	dropper.killSockets = func(ctx context.Context, _ []netip.Addr) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if dropper.DropIPs(ctx, []string{"203.0.113.10"}) {
		t.Fatal("canceled socket kill must report failure")
	}
}

func TestDropIPsAppliesOneDeadlineToWholeBatch(t *testing.T) {
	t.Parallel()

	dropper, _ := testDropper(true)
	dropper.batchTimeout = 40 * time.Millisecond
	var calls atomic.Int64
	dropper.killSockets = func(ctx context.Context, _ []netip.Addr) error {
		calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}
	started := time.Now()
	if dropper.DropIPs(context.Background(), []string{"203.0.113.10", "203.0.113.11"}) {
		t.Fatal("expired batch reported success")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("batch deadline returned after %s", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("socket killer calls = %d, want 1 before batch expiry", got)
	}
}

type fakeIPProvider struct {
	calls    atomic.Int64
	mu       sync.Mutex
	entries  map[string][]xtls.IPEntry
	err      error
	resetErr error
	resets   []bool
}

func (p *fakeIPProvider) GetUserIPList(_ context.Context, userID string, reset bool) ([]xtls.IPEntry, error) {
	p.calls.Add(1)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resets = append(p.resets, reset)
	if p.err != nil {
		return nil, p.err
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

func TestDropUsersReportsLookupFailureAndDeduplicatesUsers(t *testing.T) {
	t.Parallel()

	dropper, _ := testDropper(true)
	provider := &fakeIPProvider{err: errors.New("grpc failed")}
	if dropper.DropUsers(context.Background(), provider, []string{"u1", "u1"}) {
		t.Fatal("IP lookup failure must make the result false")
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want one for duplicate user IDs", provider.calls.Load())
	}
}

func TestDropUsersDoesNotResetStatsWithoutCapability(t *testing.T) {
	t.Parallel()

	dropper, _ := testDropper(false)
	provider := &fakeIPProvider{entries: map[string][]xtls.IPEntry{
		"u1": {{IP: "203.0.113.10"}},
	}}
	if dropper.DropUsers(context.Background(), provider, []string{"u1"}) {
		t.Fatal("missing capability must be reported")
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want zero without capability", provider.calls.Load())
	}
}

func TestDropUsersSucceedsWhenUsersHaveNoTrackedIPs(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	provider := &fakeIPProvider{entries: map[string][]xtls.IPEntry{"u1": {}}}
	if !dropper.DropUsers(context.Background(), provider, []string{"u1"}) {
		t.Fatal("a user with no tracked IPs should be a successful no-op")
	}
	if len(*killed) != 0 {
		t.Fatalf("unexpected socket kills: %v", *killed)
	}
}

func TestDropUsersRetriesSocketKillWithoutResettingOnlineStats(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	var attempts atomic.Int64
	dropper.killSockets = func(_ context.Context, targets []netip.Addr) error {
		for _, target := range targets {
			*killed = append(*killed, target.String())
		}
		if attempts.Add(1) == 1 {
			return errors.New("socket kill failed")
		}
		return nil
	}
	provider := &fakeIPProvider{entries: map[string][]xtls.IPEntry{
		"u1": {{IP: "203.0.113.10"}},
	}}

	if dropper.DropUsers(context.Background(), provider, []string{"u1"}) {
		t.Fatal("failed socket kill reported success")
	}
	provider.mu.Lock()
	firstResets := append([]bool(nil), provider.resets...)
	remaining := append([]xtls.IPEntry(nil), provider.entries["u1"]...)
	provider.mu.Unlock()
	if !slices.Equal(firstResets, []bool{false}) {
		t.Fatalf("first attempt reset flags = %v, want [false]", firstResets)
	}
	if len(remaining) != 1 {
		t.Fatalf("failed socket kill cleared IP stats: %v", remaining)
	}

	if !dropper.DropUsers(context.Background(), provider, []string{"u1"}) {
		t.Fatal("successful retry reported failure")
	}
	provider.mu.Lock()
	allResets := append([]bool(nil), provider.resets...)
	remaining = append([]xtls.IPEntry(nil), provider.entries["u1"]...)
	provider.mu.Unlock()
	if !slices.Equal(allResets, []bool{false, false}) {
		t.Fatalf("all reset flags = %v, want read-only lookups", allResets)
	}
	if len(remaining) != 1 {
		t.Fatalf("connection cleanup destructively reset online stats: %v", remaining)
	}
	if !slices.Equal(*killed, []string{"203.0.113.10", "203.0.113.10"}) {
		t.Fatalf("socket kill attempts = %v", *killed)
	}
}

func TestDropUsersNeverRequestsDestructiveOnlineStatsReset(t *testing.T) {
	t.Parallel()

	dropper, _ := testDropper(true)
	provider := &fakeIPProvider{
		entries:  map[string][]xtls.IPEntry{"u1": {{IP: "203.0.113.10"}}},
		resetErr: errors.New("reset failed"),
	}
	if !dropper.DropUsers(context.Background(), provider, []string{"u1"}) {
		t.Fatal("read-only lookup and socket kill reported failure")
	}
	provider.mu.Lock()
	resets := append([]bool(nil), provider.resets...)
	provider.mu.Unlock()
	if !slices.Equal(resets, []bool{false}) {
		t.Fatalf("reset flags = %v, want one read-only lookup", resets)
	}
}

func TestDropUsersAggregatesTargetsUnderOneAddressSnapshot(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	var batches atomic.Int64
	dropper.killSockets = func(_ context.Context, targets []netip.Addr) error {
		batches.Add(1)
		for _, target := range targets {
			*killed = append(*killed, target.String())
		}
		return nil
	}
	var snapshots atomic.Int64
	dropper.localIPSource = func() (map[netip.Addr]struct{}, error) {
		snapshots.Add(1)
		return map[netip.Addr]struct{}{}, nil
	}
	provider := &fakeIPProvider{entries: map[string][]xtls.IPEntry{
		"u1": {{IP: "203.0.113.10"}},
		"u2": {{IP: "198.51.100.20"}},
	}}

	if !dropper.DropUsers(context.Background(), provider, []string{"u1", "u2"}) {
		t.Fatal("batched connection drop reported failure")
	}
	if got := snapshots.Load(); got != 1 {
		t.Fatalf("local address snapshots = %d, want one per batch", got)
	}
	if !slices.Equal(*killed, []string{"203.0.113.10", "198.51.100.20"}) {
		t.Fatalf("socket kill targets = %v", *killed)
	}
	if got := batches.Load(); got != 1 {
		t.Fatalf("socket killer batches = %d, want 1", got)
	}
	provider.mu.Lock()
	resets := append([]bool(nil), provider.resets...)
	provider.mu.Unlock()
	if !slices.Equal(resets, []bool{false, false}) {
		t.Fatalf("reset flags = %v, want read-only lookups", resets)
	}
}

func TestDropIPsRefreshesLocalAddressesForEveryBatch(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	var snapshots atomic.Int64
	dropper.localIPSource = func() (map[netip.Addr]struct{}, error) {
		snapshots.Add(1)
		return map[netip.Addr]struct{}{}, nil
	}

	if !dropper.DropIPs(context.Background(), []string{"203.0.113.10"}) {
		t.Fatal("first batch reported failure")
	}
	if !dropper.DropIPs(context.Background(), []string{"198.51.100.20"}) {
		t.Fatal("second batch reported failure")
	}
	if got := snapshots.Load(); got != 2 {
		t.Fatalf("local address snapshots = %d, want one per batch", got)
	}
	if !slices.Equal(*killed, []string{"203.0.113.10", "198.51.100.20"}) {
		t.Fatalf("socket kill targets = %v", *killed)
	}
}

func TestDropIPsRefreshesLocalAddressProtection(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true, "198.51.100.1")
	dropper.localIPSource = func() (map[netip.Addr]struct{}, error) {
		return map[netip.Addr]struct{}{
			netip.MustParseAddr("203.0.113.10"): {},
		}, nil
	}

	if dropper.DropIPs(context.Background(), []string{"203.0.113.10"}) {
		t.Fatal("newly assigned local address reported as dropped")
	}
	if len(*killed) != 0 {
		t.Fatalf("newly assigned local address reached socket killer: %v", *killed)
	}
}

func TestDropIPsFailsClosedAndRetainsSnapshotOnRefreshFailure(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true, "203.0.113.10")
	dropper.localIPSource = func() (map[netip.Addr]struct{}, error) {
		return nil, errors.New("interface enumeration failed")
	}

	if dropper.DropIPs(context.Background(), []string{"203.0.113.10", "198.51.100.20"}) {
		t.Fatal("socket drop succeeded with an untrustworthy local-address snapshot")
	}
	if len(*killed) != 0 {
		t.Fatalf("socket killer ran with an untrustworthy local-address snapshot: %v", *killed)
	}
}

func TestDropIPsFailsClosedUntilLocalAddressesCanBeDiscovered(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	dropper.localIPsReady = false
	dropper.localIPSource = func() (map[netip.Addr]struct{}, error) {
		return nil, errors.New("interface enumeration failed")
	}

	if dropper.DropIPs(context.Background(), []string{"203.0.113.10"}) {
		t.Fatal("socket drop succeeded without a trustworthy local-address snapshot")
	}
	if len(*killed) != 0 {
		t.Fatalf("socket killer was called without a trustworthy local-address snapshot: %v", *killed)
	}
}
