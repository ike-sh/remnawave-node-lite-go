//go:build !linux

package xray

import (
	"os"
	"os/exec"
	"time"
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

func waitForProcessLeader(process *processState) (processLeaderWait, error) {
	return processLeaderWait{reaped: true, leaderErr: process.reap()}, nil
}

func cleanupOwnedProcessGroup(*os.Process, time.Duration) error { return nil }
