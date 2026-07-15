package connections

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

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
		killSockets: func(_ context.Context, ip string) error {
			mu.Lock()
			killed = append(killed, ip)
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

func TestDropIPsCanonicalizesDeduplicatesAndReportsFailure(t *testing.T) {
	t.Parallel()

	dropper, killed := testDropper(true)
	dropper.killSockets = func(_ context.Context, ip string) error {
		*killed = append(*killed, ip)
		if ip == "2001:db8::1" {
			return errors.New("ss failed")
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
	dropper.killSockets = func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if dropper.DropIPs(ctx, []string{"203.0.113.10"}) {
		t.Fatal("canceled socket kill must report failure")
	}
}

type fakeIPProvider struct {
	calls   atomic.Int64
	entries map[string][]xtls.IPEntry
	err     error
}

func (p *fakeIPProvider) GetUserIPList(_ context.Context, userID string, _ bool) ([]xtls.IPEntry, error) {
	p.calls.Add(1)
	return p.entries[userID], p.err
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
