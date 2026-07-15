//go:build linux

package xray

import (
	"os/exec"
	"syscall"
)

func configureProcessOwnership(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
