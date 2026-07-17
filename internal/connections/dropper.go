package connections

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/netadmin"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

type IPListProvider interface {
	GetUserIPList(ctx context.Context, userID string, reset bool) ([]xtls.IPEntry, error)
}

type Dropper struct {
	available     bool
	isWhitelisted func(ip string) bool
	localIPsMu    sync.RWMutex
	localIPs      map[netip.Addr]struct{}
	localIPsReady bool
	localIPSource func() (map[netip.Addr]struct{}, error)
	killSockets   func(context.Context, []netip.Addr) error
	batchTimeout  time.Duration
}

const socketKillBatchTimeout = 15 * time.Second

func NewDropper(isWhitelisted func(ip string) bool) *Dropper {
	if isWhitelisted == nil {
		isWhitelisted = func(string) bool { return false }
	}
	localIPs, err := discoverLocalIPs()
	localIPsReady := err == nil
	if err != nil {
		slog.Warn("failed to enumerate local addresses for connection-drop protection", "error", err)
		localIPs = make(map[netip.Addr]struct{})
	}
	return &Dropper{
		available:     netadmin.HasCapNetAdmin(),
		isWhitelisted: isWhitelisted,
		localIPs:      localIPs,
		localIPsReady: localIPsReady,
		localIPSource: discoverLocalIPs,
		killSockets:   netadmin.KillSocketsByIPs,
		batchTimeout:  socketKillBatchTimeout,
	}
}

func (d *Dropper) Available() bool {
	return d.available
}

func (d *Dropper) DropIPs(ctx context.Context, ips []string) bool {
	if len(ips) == 0 {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	localIPs, localIPsReady := d.refreshLocalIPs()
	if !localIPsReady {
		return false
	}
	batchTimeout := d.batchTimeout
	if batchTimeout <= 0 {
		batchTimeout = socketKillBatchTimeout
	}
	ctx, cancelBatch := context.WithTimeout(ctx, batchTimeout)
	defer cancelBatch()

	ok := true
	invalidCount := 0
	scopedCount := 0
	protectedCount := 0
	seen := make(map[netip.Addr]struct{}, len(ips))
	targets := make([]netip.Addr, 0, len(ips))
	for _, raw := range ips {
		if ctx.Err() != nil {
			return false
		}
		ip := strings.TrimSpace(raw)
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			invalidCount++
			ok = false
			continue
		}
		if addr.Zone() != "" {
			scopedCount++
			ok = false
			continue
		}
		addr = addr.Unmap()
		canonical := addr.String()
		if d.isWhitelisted(raw) || d.isWhitelisted(canonical) {
			continue
		}
		if isProtectedAddress(addr, localIPs) {
			protectedCount++
			ok = false
			continue
		}
		if _, duplicate := seen[addr]; duplicate {
			continue
		}
		seen[addr] = struct{}{}
		targets = append(targets, addr)
	}
	if invalidCount != 0 || scopedCount != 0 || protectedCount != 0 {
		slog.Warn(
			"refused unsafe connection-drop targets",
			"invalidCount", invalidCount,
			"scopedCount", scopedCount,
			"protectedCount", protectedCount,
		)
	}
	if len(targets) == 0 {
		return ok
	}
	if !d.available || d.killSockets == nil {
		return false
	}
	if err := d.killSockets(ctx, targets); err != nil {
		slog.Warn("failed to drop connections", "targetCount", len(targets), "error", err)
		return false
	}
	return ok
}

func (d *Dropper) DropUsers(ctx context.Context, provider IPListProvider, userIDs []string) bool {
	if len(userIDs) == 0 {
		return true
	}
	if !d.available || provider == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	batchTimeout := d.batchTimeout
	if batchTimeout <= 0 {
		batchTimeout = socketKillBatchTimeout
	}
	ctx, cancelBatch := context.WithTimeout(ctx, batchTimeout)
	defer cancelBatch()

	ok := true
	seen := make(map[string]struct{}, len(userIDs))
	allIPs := make([]string, 0)
	for _, userID := range userIDs {
		if _, duplicate := seen[userID]; duplicate {
			continue
		}
		seen[userID] = struct{}{}
		if err := ctx.Err(); err != nil {
			return false
		}
		// The pinned core removes online IPs with connection refcounts and ignores
		// reset. Keep this read non-destructive for reset-capable compatible cores.
		entries, err := provider.GetUserIPList(ctx, userID, false)
		if err != nil {
			slog.Warn("failed to get user IPs before dropping connections", "userId", userID, "error", err)
			ok = false
			continue
		}
		if len(entries) == 0 {
			continue
		}
		for _, entry := range entries {
			if entry.IP != "" {
				allIPs = append(allIPs, entry.IP)
			}
		}
	}
	if len(allIPs) == 0 {
		return ok
	}
	if !d.DropIPs(ctx, allIPs) {
		return false
	}
	return ok
}

func isProtectedAddress(addr netip.Addr, localIPs map[netip.Addr]struct{}) bool {
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() ||
		addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}
	if addr.Is4() && addr == netip.MustParseAddr("255.255.255.255") {
		return true
	}
	_, local := localIPs[addr]
	return local
}

func (d *Dropper) refreshLocalIPs() (map[netip.Addr]struct{}, bool) {
	if d == nil {
		return nil, false
	}
	if d.localIPSource == nil {
		d.localIPsMu.RLock()
		defer d.localIPsMu.RUnlock()
		return d.localIPs, d.localIPsReady
	}
	d.localIPsMu.Lock()
	defer d.localIPsMu.Unlock()
	localIPs, err := d.localIPSource()
	if err != nil {
		d.localIPsReady = false
		slog.Warn("failed to refresh local addresses for connection-drop protection", "error", err)
		return nil, false
	}
	d.localIPs = localIPs
	d.localIPsReady = true
	return localIPs, true
}

func discoverLocalIPs() (map[netip.Addr]struct{}, error) {
	result := make(map[netip.Addr]struct{})
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interface addresses: %w", err)
	}
	for _, address := range addresses {
		host := address.String()
		if slash := strings.LastIndexByte(host, '/'); slash >= 0 {
			host = host[:slash]
		}
		if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
			host = host[:zone]
		}
		addr, err := netip.ParseAddr(host)
		if err == nil {
			result[addr.Unmap()] = struct{}{}
		}
	}
	return result, nil
}
