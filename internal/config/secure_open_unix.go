//go:build linux || darwin

package config

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openReadOnlyNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		if closeErr := unix.Close(fd); closeErr != nil {
			return nil, fmt.Errorf("create file handle and close fd: %w", closeErr)
		}
		return nil, errors.New("create file handle")
	}
	return file, nil
}
