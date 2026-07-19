//go:build linux

package xray

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	processGroupPollInterval = 10 * time.Millisecond
	maxProcStatBytes         = 4 << 10
	maxReportedGroupMembers  = 8
)

var errProcessGroupScanTimeout = errors.New("process group scan deadline exceeded")

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

// waitForProcessLeader observes the leader without reaping it. Keeping the
// zombie leader reserves its PID while its process group is cleaned, so that a
// recycled PID can never redirect a later group signal to an unrelated group.
func waitForProcessLeader(process *processState) (processLeaderWait, error) {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return processLeaderWait{}, errors.New("rw-core leader process is unavailable")
	}
	cmd := process.cmd
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, cmd.Process.Pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return processLeaderWait{}, fmt.Errorf("waitid rw-core leader %d: %w", cmd.Process.Pid, err)
		}
		return processLeaderWait{}, nil
	}
}

// cleanupOwnedProcessGroup kills every live member of the group and verifies
// that no live non-leader member remains before cmd.Wait is allowed to reap the
// leader. Zombie members have already released their resources and cannot
// execute or receive signals, so waiting for an external reaper would make
// cleanup depend on the container's PID 1.
func cleanupOwnedProcessGroup(process *os.Process, timeout time.Duration) error {
	if process == nil {
		return os.ErrProcessDone
	}
	err := killOwnedProcess(process)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	deadline := time.Now().Add(timeout)
	for {
		members, scanErr := ownedProcessGroupMembers(process.Pid, process.Pid, deadline)
		if errors.Is(scanErr, errProcessGroupScanTimeout) {
			return fmt.Errorf("verify rw-core process group %d: timed out scanning members %s", process.Pid, formatProcessGroupMembers(members))
		}
		if scanErr != nil {
			return fmt.Errorf("verify rw-core process group %d: %w", process.Pid, scanErr)
		}
		if members.total == 0 {
			return nil
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return fmt.Errorf("verify rw-core process group %d: timed out with members %s", process.Pid, formatProcessGroupMembers(members))
		}

		remaining := time.Until(deadline)
		if remaining > processGroupPollInterval {
			remaining = processGroupPollInterval
		}
		time.Sleep(remaining)
	}
}

type processGroupMember struct {
	pid   int
	state byte
}

type processGroupSnapshot struct {
	total         int
	reported      [maxReportedGroupMembers]processGroupMember
	reportedCount int
}

func ownedProcessGroupMembers(processGroup, leaderPID int, deadline time.Time) (processGroupSnapshot, error) {
	proc, err := os.Open("/proc")
	if err != nil {
		return processGroupSnapshot{}, err
	}
	defer proc.Close()

	var members processGroupSnapshot
	for {
		entries, readErr := proc.ReadDir(128)
		for _, entry := range entries {
			if !time.Now().Before(deadline) {
				return members, errProcessGroupScanTimeout
			}
			pid, parseErr := strconv.Atoi(entry.Name())
			if parseErr != nil || pid == leaderPID {
				continue
			}
			state, pgid, statErr := readLinuxProcessStat(pid)
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			if statErr != nil {
				return processGroupSnapshot{}, statErr
			}
			if pgid == processGroup && state != 'Z' && state != 'X' {
				members.total++
				if members.reportedCount < len(members.reported) {
					members.reported[members.reportedCount] = processGroupMember{pid: pid, state: state}
					members.reportedCount++
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return processGroupSnapshot{}, readErr
		}
	}
	sort.Slice(members.reported[:members.reportedCount], func(i, j int) bool {
		return members.reported[i].pid < members.reported[j].pid
	})
	return members, nil
}

func readLinuxProcessStat(pid int) (state byte, processGroup int, err error) {
	path := "/proc/" + strconv.Itoa(pid) + "/stat"
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	raw, err := io.ReadAll(io.LimitReader(file, maxProcStatBytes+1))
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxProcStatBytes {
		return 0, 0, fmt.Errorf("read %s: stat exceeds %d bytes", path, maxProcStatBytes)
	}
	closeParen := bytes.LastIndex(raw, []byte(") "))
	if closeParen < 0 {
		return 0, 0, fmt.Errorf("parse %s: missing command terminator", path)
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	if len(fields) < 3 || len(fields[0]) != 1 {
		return 0, 0, fmt.Errorf("parse %s: incomplete stat", path)
	}
	processGroup, err = strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, fmt.Errorf("parse %s process group: %w", path, err)
	}
	return fields[0][0], processGroup, nil
}

func formatProcessGroupMembers(members processGroupSnapshot) string {
	if members.total == 0 {
		return "none observed before deadline"
	}
	parts := make([]string, 0, members.reportedCount+1)
	for _, member := range members.reported[:members.reportedCount] {
		parts = append(parts, strconv.Itoa(member.pid)+"("+string(member.state)+")")
	}
	if members.total > members.reportedCount {
		parts = append(parts, fmt.Sprintf("+%d more", members.total-members.reportedCount))
	}
	return strings.Join(parts, ",")
}

func normalizeProcessGroupError(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
