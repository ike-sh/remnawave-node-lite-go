//go:build linux

package xray

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

func TestCleanupFailureRetainsLeaderAndConcurrentStopRetries(t *testing.T) {
	manager, helper := newLifecycleManager(t, "exit-immediately")
	manager.readinessProbe = func(context.Context) bool { return false }
	cleanupFailure := errors.New("injected process-group verification failure")
	var failedAttempts atomic.Int32
	manager.mu.Lock()
	manager.processGroupCleanup = func(*os.Process, time.Duration) error {
		failedAttempts.Add(1)
		return cleanupFailure
	}
	manager.mu.Unlock()

	releaseRetry := make(chan struct{})
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(releaseRetry) })
		manager.mu.Lock()
		manager.processGroupCleanup = cleanupOwnedProcessGroup
		manager.mu.Unlock()
		_ = manager.Stop()
	}()

	response := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if response.IsStarted || response.Error == nil || !strings.Contains(*response.Error, cleanupFailure.Error()) {
		t.Fatalf("start response after cleanup failure = %#v", response)
	}

	manager.mu.RLock()
	process := manager.process
	state := manager.state
	manager.mu.RUnlock()
	if state != lifecycleStopping || process == nil || process.cmd.Process == nil {
		t.Fatalf("cleanup failure state = %s process=%v", state, process != nil)
	}
	leaderPID := process.cmd.Process.Pid
	leaderState, processGroup, err := readLinuxProcessStat(leaderPID)
	if err != nil {
		t.Fatalf("read retained leader %d: %v", leaderPID, err)
	}
	if leaderState != 'Z' || processGroup != leaderPID {
		t.Fatalf("retained leader %d state=%q processGroup=%d", leaderPID, leaderState, processGroup)
	}
	outcome := process.outcome()
	if outcome.finalized || outcome.cleanupVerified || !errors.Is(outcome.cleanupErr, cleanupFailure) || outcome.leaderErr != nil {
		t.Fatalf("outcome after cleanup failure = %#v", outcome)
	}

	if stopped := manager.Stop(); stopped.IsStopped {
		t.Fatalf("failed cleanup reported stopped: %#v", stopped)
	}
	if got := failedAttempts.Load(); got != 2 {
		t.Fatalf("cleanup attempts = %d, want 2", got)
	}
	if _, _, err := readLinuxProcessStat(leaderPID); err != nil {
		t.Fatalf("failed Stop reaped identity anchor %d: %v", leaderPID, err)
	}

	blocked := manager.Start(context.Background(), lifecycleStartRequest("client-b"))
	if blocked.IsStarted || blocked.Error == nil || *blocked.Error != "Request already in progress" {
		t.Fatalf("Start while cleanup is unresolved = %#v", blocked)
	}
	if got := helper.starts.Load(); got != 1 {
		t.Fatalf("spawn count while cleanup unresolved = %d, want 1", got)
	}

	retryEntered := make(chan struct{})
	var retryEnteredOnce sync.Once
	var retryAttempts atomic.Int32
	manager.mu.Lock()
	manager.processGroupCleanup = func(process *os.Process, timeout time.Duration) error {
		retryAttempts.Add(1)
		retryEnteredOnce.Do(func() { close(retryEntered) })
		<-releaseRetry
		return cleanupOwnedProcessGroup(process, timeout)
	}
	manager.mu.Unlock()

	first := make(chan StopResponse, 1)
	go func() { first <- manager.Stop() }()
	awaitSignal(t, retryEntered, "cleanup retry")
	waitForStopOperation(t, manager)
	second := make(chan StopResponse, 1)
	go func() { second <- manager.Stop() }()
	releaseOnce.Do(func() { close(releaseRetry) })

	for index, result := range []<-chan StopResponse{first, second} {
		select {
		case stopped := <-result:
			if !stopped.IsStopped {
				t.Fatalf("retried Stop %d = %#v", index+1, stopped)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for retried Stop %d", index+1)
		}
	}
	if got := retryAttempts.Load(); got != 1 {
		t.Fatalf("concurrent cleanup retries = %d, want 1", got)
	}

	outcome = process.outcome()
	if !outcome.finalized || !outcome.cleanupVerified || !errors.Is(outcome.cleanupErr, cleanupFailure) {
		t.Fatalf("final outcome = %#v", outcome)
	}
	if outcome.leaderErr == nil || !strings.Contains(outcome.leaderErr.Error(), "exit status 23") {
		t.Fatalf("leader error was not recorded separately: %v", outcome.leaderErr)
	}
	if _, _, err := readLinuxProcessStat(leaderPID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leader identity anchor %d still exists after verified cleanup: %v", leaderPID, err)
	}
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
