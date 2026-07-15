//go:build !linux

package netadmin

import (
	"context"
	"errors"
)

func KillSocketsByIP(context.Context, string) error {
	return errors.New("socket destruction is only supported on Linux")
}
