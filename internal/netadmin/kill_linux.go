//go:build linux

package netadmin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/executil"
)

const socketKillCommandTimeout = 3 * time.Second

// KillSocketsByIP closes TCP sockets where ip matches source or destination.
func KillSocketsByIP(ctx context.Context, ip string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("parse socket-kill IP %q: %w", ip, err)
	}
	addr = addr.Unmap()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, socketKillCommandTimeout)
	defer cancel()

	family := "-4"
	target := addr.String()
	if !addr.Is4() {
		family = "-6"
		// ss treats the final colon-separated component as a port unless an
		// IPv6 address is bracketed in its filter expression.
		target = "[" + target + "]"
	}

	var errs []error
	for _, direction := range []string{"src", "dst"} {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		result, err := executil.Run(ctx, nil, 512, "ss", family, "-K", direction, target)
		if err != nil {
			detail := strings.TrimSpace(string(result.DiagnosticOutput()))
			if result.AnyTruncated() {
				detail += "..."
			}
			if detail != "" {
				err = fmt.Errorf("ss -K %s %s: %w: %s", direction, addr, err, detail)
			} else {
				err = fmt.Errorf("ss -K %s %s: %w", direction, addr, err)
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
