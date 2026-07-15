//go:build !linux

package xray

import "os/exec"

func configureProcessOwnership(_ *exec.Cmd) {}
