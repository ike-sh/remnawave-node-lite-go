package connections

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strings"
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
	localIPs      map[netip.Addr]struct{}
	killSockets   func(context.Context, string) error
	socketTimeout time.Duration
	batchTimeout  time.Duration
}

const (
	socketKillTimeout      = 3 * time.Second
	socketKillBatchTimeout = 15 * time.Second
)

func NewDropper(isWhitelisted func(ip string) bool) *Dropper {
	if isWhitelisted == nil {
		isWhitelisted = func(string) bool { return false }
	}
	return &Dropper{
		available:     netadmin.HasCapNetAdmin(),
		isWhitelisted: isWhitelisted,
		localIPs:      discoverLocalIPs(),
		killSockets:   netadmin.KillSocketsByIP,
		socketTimeout: socketKillTimeout,
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
	batchTimeout := d.batchTimeout
	if batchTimeout <= 0 {
		batchTimeout = socketKillBatchTimeout
	}
	ctx, cancelBatch := context.WithTimeout(ctx, batchTimeout)
	defer cancelBatch()
	socketTimeout := d.socketTimeout
	if socketTimeout <= 0 {
		socketTimeout = socketKillTimeout
	}

	ok := true
	seen := make(map[netip.Addr]struct{}, len(ips))
	for _, raw := range ips {
		if ctx.Err() != nil {
			return false
		}
		ip := strings.TrimSpace(raw)
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			slog.Warn("refusing to drop connections for invalid IP", "ip", raw)
			ok = false
			continue
		}
		addr = addr.Unmap()
		canonical := addr.String()
		if d.isWhitelisted(raw) || d.isWhitelisted(canonical) {
			continue
		}
		if d.isProtected(addr) {
			slog.Warn("refusing to drop connections for protected IP", "ip", canonical)
			ok = false
			continue
		}
		if _, duplicate := seen[addr]; duplicate {
			continue
		}
		seen[addr] = struct{}{}
		if !d.available || d.killSockets == nil {
			ok = false
			continue
		}

		killCtx, cancel := context.WithTimeout(ctx, socketTimeout)
		err = d.killSockets(killCtx, canonical)
		cancel()
		if err != nil {
			slog.Warn("failed to drop connections", "ip", canonical, "error", err)
			ok = false
		}
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
	for _, userID := range userIDs {
		if _, duplicate := seen[userID]; duplicate {
			continue
		}
		seen[userID] = struct{}{}
		if err := ctx.Err(); err != nil {
			return false
		}
		entries, err := provider.GetUserIPList(ctx, userID, true)
		if err != nil {
			slog.Warn("failed to get user IPs before dropping connections", "userId", userID, "error", err)
			ok = false
			continue
		}
		if len(entries) == 0 {
			continue
		}
		ips := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IP != "" {
				ips = append(ips, entry.IP)
			}
		}
		if !d.DropIPs(ctx, ips) {
			ok = false
		}
	}
	return ok
}

func (d *Dropper) isProtected(addr netip.Addr) bool {
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsLoopback() ||
		addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return true
	}
	if addr.Is4() && addr == netip.MustParseAddr("255.255.255.255") {
		return true
	}
	_, local := d.localIPs[addr]
	return local
}

func discoverLocalIPs() map[netip.Addr]struct{} {
	result := make(map[netip.Addr]struct{})
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		slog.Warn("failed to enumerate local addresses for connection-drop protection", "error", err)
		return result
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
	return result
}
