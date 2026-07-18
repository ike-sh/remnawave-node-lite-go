//go:build !linux && !darwin

package config

import (
	"errors"
	"os"
)

func openReadOnlyNoFollow(string) (*os.File, error) {
	return nil, errors.New("secure non-following file reads are unsupported on this platform")
}
