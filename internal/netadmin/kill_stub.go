//go:build !linux

package netadmin

import (
	"context"
	"errors"
	"net/netip"
)

func KillSocketsByIP(context.Context, string) error {
	return errors.New("socket destruction is only supported on Linux")
}

func KillSocketsByIPs(_ context.Context, addresses []netip.Addr) error {
	if len(addresses) == 0 {
		return nil
	}
	return errors.New("socket destruction is only supported on Linux")
}
