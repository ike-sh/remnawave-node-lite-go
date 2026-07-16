//go:build !linux

package xray

import (
	"os"
	"os/exec"
)

func configureProcessOwnership(_ *exec.Cmd) {}

func signalOwnedProcess(process *os.Process, signal os.Signal) error {
	if process == nil {
		return os.ErrProcessDone
	}
	return process.Signal(signal)
}

func killOwnedProcess(process *os.Process) error {
	if process == nil {
		return os.ErrProcessDone
	}
	return process.Kill()
}

func cleanupOwnedProcessGroup(*os.Process) error { return nil }
