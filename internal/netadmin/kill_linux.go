//go:build linux

package netadmin

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
)

// KillSocketsByIP closes TCP sockets where ip matches source or destination.
func KillSocketsByIP(ctx context.Context, ip string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("parse socket-kill IP %q: %w", ip, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	family := "-4"
	if !addr.Unmap().Is4() {
		family = "-6"
	}

	var errs []error
	for _, direction := range []string{"src", "dst"} {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		output, err := exec.CommandContext(ctx, "ss", family, "-K", direction, addr.String()).CombinedOutput()
		if err != nil {
			detail := strings.TrimSpace(string(output))
			if len(detail) > 512 {
				detail = detail[:512]
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
