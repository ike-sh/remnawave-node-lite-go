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
	available   bool
	isWhitelisted func(ip string) bool
}

func NewDropper(isWhitelisted func(ip string) bool) *Dropper {
	if isWhitelisted == nil {
		isWhitelisted = func(string) bool { return false }
	}
	return &Dropper{
		available:     netadmin.HasCapNetAdmin(),
		isWhitelisted: isWhitelisted,
	}
}

func (d *Dropper) Available() bool {
	return d.available
}

func (d *Dropper) DropIPs(ips []string) bool {
	if !d.available || len(ips) == 0 {
		return true
	}
	ok := true
	for _, ip := range ips {
		if ip == "" || d.isWhitelisted(ip) {
			continue
		}
		if err := netadmin.KillSocketsByIP(ip); err != nil {
			slog.Warn("failed to drop connections", "ip", ip, "error", err)
			ok = false
		}
	}
	return ok
}

func (d *Dropper) DropUsers(ctx context.Context, provider IPListProvider, userIDs []string) bool {
	if !d.available || provider == nil {
		return true
	}
	ok := true
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
		if !d.DropIPs(ips) {
			ok = false
		}
	}
	return ok
}
