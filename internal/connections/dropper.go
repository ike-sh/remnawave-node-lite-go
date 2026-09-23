package connections

import (
	"context"
	"log/slog"

	"remnawave-node-lite-go/internal/netadmin"
	"remnawave-node-lite-go/internal/xtls"
)

type IPListProvider interface {
	GetUserIPList(ctx context.Context, userID string, reset bool) ([]xtls.IPEntry, error)
}

type Dropper struct {
	available     bool
	isWhitelisted func(ip string) bool
	kill          func(ip string) error
}

func NewDropper(isWhitelisted func(ip string) bool) *Dropper {
	if isWhitelisted == nil {
		isWhitelisted = func(string) bool { return false }
	}
	return &Dropper{
		available:     netadmin.HasCapNetAdmin(),
		isWhitelisted: isWhitelisted,
		kill:          netadmin.KillSocketsByIP,
	}
}

func (d *Dropper) Available() bool {
	return d.available
}

func (d *Dropper) DropIPs(ips []string) bool {
	if !d.available || len(ips) == 0 {
		return true
	}
	seen := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		if ip == "" || d.isWhitelisted(ip) {
			continue
		}
		if _, duplicate := seen[ip]; duplicate {
			continue
		}
		seen[ip] = struct{}{}
		if err := d.kill(ip); err != nil {
			slog.Warn("failed to drop connections", "ip", ip, "error", err)
		}
	}
	// Official v3.4.1 publishes an async event and reports accepted even when
	// a socket kill later fails; keep the REST result independent of that failure.
	return true
}

func (d *Dropper) DropUsers(ctx context.Context, provider IPListProvider, userIDs []string) bool {
	if !d.available || provider == nil {
		return true
	}
	for _, userID := range userIDs {
		entries, err := provider.GetUserIPList(ctx, userID, true)
		if err != nil || len(entries) == 0 {
			continue
		}
		ips := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IP != "" {
				ips = append(ips, entry.IP)
			}
		}
		d.DropIPs(ips)
	}
	return true
}
