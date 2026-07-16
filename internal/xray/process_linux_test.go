//go:build linux

package xray

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExitedWrapperDoesNotLeaveInheritedStdioChild(t *testing.T) {
	manager, process := newLifecycleManager(t, "exit-with-stdio-child")
	manager.readinessProbe = func(context.Context) bool { return false }
	response := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if response.IsStarted {
		t.Fatalf("unexpected start response: %#v", response)
	}

	pid := waitForHelperChildPID(t, process.events)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !linuxProcessRunning(pid) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if child, err := os.FindProcess(pid); err == nil {
		_ = child.Kill()
	}
	t.Fatalf("rw-core wrapper child %d remained alive after its process group was reaped", pid)
}

func waitForHelperChildPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		raw, _ := os.ReadFile(path)
		for _, line := range strings.Split(string(raw), "\n") {
			if value, ok := strings.CutPrefix(line, "child-pid="); ok {
				pid, err := strconv.Atoi(value)
				if err == nil && pid > 0 {
					return pid
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for wrapper child pid in %s", path)
	return 0
}

func linuxProcessRunning(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(raw))
	return len(fields) < 3 || fields[2] != "Z"
}
