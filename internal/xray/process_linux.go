//go:build linux

package xray

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessOwnership(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}

func signalOwnedProcess(process *os.Process, signal os.Signal) error {
	if process == nil {
		return os.ErrProcessDone
	}
	systemSignal, ok := signal.(syscall.Signal)
	if !ok {
		return process.Signal(signal)
	}
	return normalizeProcessGroupError(syscall.Kill(-process.Pid, systemSignal))
}

func killOwnedProcess(process *os.Process) error {
	if process == nil {
		return os.ErrProcessDone
	}
	return normalizeProcessGroupError(syscall.Kill(-process.Pid, syscall.SIGKILL))
}

func cleanupOwnedProcessGroup(process *os.Process) error {
	err := killOwnedProcess(process)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func normalizeProcessGroupError(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
