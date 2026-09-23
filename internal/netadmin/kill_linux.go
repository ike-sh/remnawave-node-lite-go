//go:build linux

package netadmin

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// KillSocketsByIP closes TCP sockets where ip matches source or destination.
func KillSocketsByIP(ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid IP %q", ip)
	}

	family := "-4"
	filterIP := parsed.String()
	if parsed.To4() == nil {
		family = "-6"
		filterIP = "[" + ip + "]"
	}

	var failures []error
	for _, direction := range []string{"src", "dst"} {
		cmd := exec.Command("ss", family, "-t", "-K", direction, filterIP)
		if output, err := cmd.CombinedOutput(); err != nil {
			failures = append(failures, fmt.Errorf("ss -K %s %s: %w: %s", direction, ip, err, string(output)))
			continue
		}
		// ss -K exits successfully even when the kernel silently skips a socket
		// it cannot destroy. Its manual says only destroyed sockets are printed.
		query := exec.Command("ss", family, "-H", "-tn", direction, filterIP)
		remaining, err := query.CombinedOutput()
		if err != nil {
			failures = append(failures, fmt.Errorf("verify ss -K %s %s: %w: %s", direction, ip, err, string(remaining)))
		} else if strings.TrimSpace(string(remaining)) != "" {
			failures = append(failures, fmt.Errorf("TCP sockets remain after ss -K %s %s (kernel may not support SOCK_DESTROY)", direction, ip))
		}
	}
	return errors.Join(failures...)
}
