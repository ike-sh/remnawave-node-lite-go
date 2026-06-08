//go:build linux

package netadmin

import (
	"log/slog"
	"net"
	"os/exec"
)

// KillSocketsByIP closes TCP sockets where ip matches source or destination.
func KillSocketsByIP(ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil
	}

	family := "-4"
	if parsed.To4() == nil {
		family = "-6"
	}

	for _, direction := range []string{"src", "dst"} {
		cmd := exec.Command("ss", family, "-K", direction, ip)
		if err := cmd.Run(); err != nil {
			slog.Debug("ss kill sockets", "direction", direction, "ip", ip, "error", err)
		}
	}
	return nil
}
