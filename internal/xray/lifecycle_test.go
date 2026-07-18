package xray

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const helperProcessEnv = "GO_WANT_XRAY_PROCESS_HELPER"

func TestXrayProcessHelper(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}

	events := os.Getenv("XRAY_HELPER_EVENTS")
	appendHelperEvent(events, "started\n")
	switch os.Getenv("XRAY_HELPER_MODE") {
	case "exit-immediately":
		os.Exit(23)
	case "exit-after":
		delay, _ := time.ParseDuration(os.Getenv("XRAY_HELPER_DELAY"))
		time.Sleep(delay)
		os.Exit(23)
	case "ignore-interrupt":
		signal.Ignore(os.Interrupt)
		appendHelperEvent(events, "ready\n")
		for {
			time.Sleep(time.Second)
		}
	case "exit-with-stdio-child":
		child := exec.Command(os.Args[0], "-test.run=^TestXrayProcessHelper$", "--")
		child.Env = append(os.Environ(),
			helperProcessEnv+"=1",
			"XRAY_HELPER_MODE=stdio-child",
		)
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(24)
		}
		appendHelperEvent(events, "child-pid="+strconv.Itoa(child.Process.Pid)+"\n")
		os.Exit(0)
	case "stdio-child":
		time.Sleep(2 * time.Second)
		os.Exit(0)
	default:
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		appendHelperEvent(events, "ready\n")
		<-signals
		appendHelperEvent(events, "interrupt\n")
		os.Exit(0)
	}
}

func appendHelperEvent(path, event string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = file.WriteString(event)
	_ = file.Close()
}

type testProcess struct {
	events string
	starts atomic.Int32
}

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

func newLifecycleManager(t *testing.T, mode string) (*Manager, *testProcess) {
	t.Helper()
	manager, err := NewManager(Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             t.TempDir(),
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave-test.sock",
		InternalRESTToken:  "token",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	process := &testProcess{events: filepath.Join(t.TempDir(), "events.log")}
	manager.processCommand = func() *exec.Cmd {
		process.starts.Add(1)
		cmd := exec.Command(os.Args[0], "-test.run=^TestXrayProcessHelper$", "--")
		cmd.Env = append(os.Environ(),
			helperProcessEnv+"=1",
			"XRAY_HELPER_MODE="+mode,
			"XRAY_HELPER_EVENTS="+process.events,
			"XRAY_HELPER_DELAY=150ms",
		)
		return cmd
	}
	manager.readinessInterval = 5 * time.Millisecond
	manager.startupTimeout = 500 * time.Millisecond
	manager.interruptTimeout = 500 * time.Millisecond
	manager.killTimeout = time.Second
	if mode == "exit-with-stdio-child" {
		manager.processWaitDelay = 50 * time.Millisecond
	}
	t.Cleanup(func() {
		manager.interruptTimeout = 50 * time.Millisecond
		manager.killTimeout = time.Second
		_ = manager.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	return manager, process
}

func lifecycleStartRequest(clientID string) StartRequest {
	tag := "public"
	return StartRequest{
		Internals: StartInternals{Hashes: ConfigHash{
			EmptyConfig: "base-hash",
			Inbounds: []InboundHash{{
				UsersCount: 1,
				Hash:       NewHashedSet(clientID).Hash64String(),
				Tag:        tag,
			}},
		}},
		XrayConfig: map[string]any{
			"inbounds": []any{map[string]any{
				"tag": tag,
				"settings": map[string]any{
					"clients": []any{map[string]any{"id": clientID}},
				},
			}},
		},
	}
}

func TestStartCommitsConfigOnlyAfterReadiness(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	probeEntered := make(chan struct{})
	allowReady := make(chan struct{})
	var once sync.Once
	manager.readinessProbe = func(ctx context.Context) bool {
		once.Do(func() { close(probeEntered) })
		select {
		case <-allowReady:
			return true
		case <-ctx.Done():
			return false
		}

	}

	response := make(chan StartResponse, 1)
	go func() { response <- manager.Start(context.Background(), lifecycleStartRequest("client-a")) }()
	awaitSignal(t, probeEntered, "readiness probe")

	manager.mu.RLock()
	if manager.state != lifecycleStarting || len(manager.pendingConfigJSON) == 0 {
		t.Fatalf("unexpected starting snapshot: state=%s pending=%v", manager.state, len(manager.pendingConfigJSON) != 0)
	}
	if manager.emptyConfigHash != "" || len(manager.inboundHashes) != 0 {
		t.Fatalf("hash state committed before readiness: empty=%q inbounds=%d", manager.emptyConfigHash, len(manager.inboundHashes))
	}
	manager.mu.RUnlock()

	raw := manager.CurrentConfigJSON()
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil || config["inbounds"] == nil {
		t.Fatalf("pending config is not served to rw-core: %s (%v)", raw, err)
	}

	close(allowReady)
	resp := awaitStartResponse(t, response)
	if !resp.IsStarted || resp.Error != nil {
		t.Fatalf("start response = %#v", resp)
	}

	manager.mu.RLock()
	state := manager.state
	pending := len(manager.pendingConfigJSON) != 0
	emptyHash := manager.emptyConfigHash
	inboundCount := len(manager.inboundHashes)
	manager.mu.RUnlock()
	if state != lifecycleRunning || pending {
		t.Fatalf("unexpected committed snapshot: state=%s pending=%v", state, pending)
	}
	if emptyHash != "base-hash" || inboundCount != 1 {
		t.Fatalf("hash state not committed: empty=%q inbounds=%d", emptyHash, inboundCount)
	}
	if got := string(manager.CurrentConfigJSON()); got != "{}" {
		t.Fatalf("config cache retained after readiness: %s", got)
	}
}

func TestSuccessfulStartRefreshesCoreVersion(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	manager.versionProbe = func(context.Context) (string, error) {
		return "26.6.27", nil
	}

	response := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if !response.IsStarted || response.Version == nil || *response.Version != "26.6.27" {
		t.Fatalf("start response version = %#v", response.Version)
	}
	if health := manager.Health(); health.XrayVersion == nil || *health.XrayVersion != "26.6.27" {
		t.Fatalf("health version = %#v", health.XrayVersion)
	}
}

func TestStartCancellationDuringVersionProbeDoesNotCommit(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	probeEntered := make(chan struct{})
	manager.versionProbe = func(ctx context.Context) (string, error) {
		close(probeEntered)
		<-ctx.Done()
		return "", ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	response := make(chan StartResponse, 1)
	go func() { response <- manager.Start(ctx, lifecycleStartRequest("client-a")) }()
	awaitSignal(t, probeEntered, "version probe")
	cancel()
	resp := awaitStartResponse(t, response)
	if resp.IsStarted || resp.Error == nil || !strings.Contains(*resp.Error, context.Canceled.Error()) {
		t.Fatalf("canceled version probe response = %#v", resp)
	}
	assertStoppedAndCleared(t, manager)
}

func TestVersionProbePublishesOnlyWithRunningState(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	oldVersion := "old"
	manager.mu.Lock()
	manager.xrayVersion = &oldVersion
	manager.mu.Unlock()
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	manager.versionProbe = func(context.Context) (string, error) {
		close(probeEntered)
		<-releaseProbe
		return "26.6.27", nil
	}

	response := make(chan StartResponse, 1)
	go func() { response <- manager.Start(context.Background(), lifecycleStartRequest("client-a")) }()
	awaitSignal(t, probeEntered, "version probe")
	if health := manager.Health(); health.XrayVersion == nil || *health.XrayVersion != oldVersion || health.XrayInternalStatusCached {
		t.Fatalf("version was published before lifecycle commit: %#v", health)
	}
	close(releaseProbe)
	if resp := awaitStartResponse(t, response); !resp.IsStarted || resp.Version == nil || *resp.Version != "26.6.27" {
		t.Fatalf("start response = %#v", resp)
	}
}

func TestSuccessfulStartClearsStaleVersionWhenProbeFails(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	oldVersion := "old"
	manager.mu.Lock()
	manager.xrayVersion = &oldVersion
	manager.mu.Unlock()
	manager.versionProbe = func(context.Context) (string, error) {
		return "", errors.New("version failed")
	}

	response := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if !response.IsStarted || response.Version != nil {
		t.Fatalf("start response = %#v", response)
	}
}

func TestHealthRetriesUnknownVersionWithSingleFlight(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	var probes atomic.Int32
	manager.versionProbe = func(context.Context) (string, error) {
		probes.Add(1)
		close(probeEntered)
		<-releaseProbe
		return "26.6.27", nil
	}
	manager.mu.Lock()
	manager.xrayVersion = nil
	manager.nextVersionProbe = time.Time{}
	manager.mu.Unlock()

	for range 8 {
		_ = manager.Health()
	}
	awaitSignal(t, probeEntered, "health version retry")
	if got := probes.Load(); got != 1 {
		t.Fatalf("version probes = %d, want 1", got)
	}
	close(releaseProbe)
	deadline := time.Now().Add(time.Second)
	for {
		if health := manager.Health(); health.XrayVersion != nil && *health.XrayVersion == "26.6.27" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("health retry did not publish version")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShutdownCancelsAndWaitsForHealthVersionProbe(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	probeEntered := make(chan struct{})
	probeExited := make(chan struct{})
	var probes atomic.Int32
	manager.versionProbe = func(ctx context.Context) (string, error) {
		probes.Add(1)
		close(probeEntered)
		<-ctx.Done()
		close(probeExited)
		return "", ctx.Err()
	}
	manager.mu.Lock()
	manager.xrayVersion = nil
	manager.nextVersionProbe = time.Time{}
	manager.mu.Unlock()

	_ = manager.Health()
	awaitSignal(t, probeEntered, "health version retry")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	awaitSignal(t, probeExited, "canceled health version retry")

	manager.mu.RLock()
	busy := manager.versionProbeBusy
	shutdown := manager.versionProbeShutdown
	manager.mu.RUnlock()
	if busy || !shutdown {
		t.Fatalf("version probe state after shutdown: busy=%v shutdown=%v", busy, shutdown)
	}
	_ = manager.Health()
	if got := probes.Load(); got != 1 {
		t.Fatalf("version probes after shutdown = %d, want 1", got)
	}
}

func TestStaleHealthVersionProbeCannotCrossStartGeneration(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	staleProbeEntered := make(chan struct{})
	releaseStaleProbe := make(chan struct{})
	manager.mu.Lock()
	manager.xrayVersion = nil
	manager.nextVersionProbe = time.Time{}
	manager.versionProbe = func(context.Context) (string, error) {
		close(staleProbeEntered)
		<-releaseStaleProbe
		return "1.2.3", nil
	}
	manager.mu.Unlock()
	_ = manager.Health()
	awaitSignal(t, staleProbeEntered, "stale health version probe")

	manager.mu.Lock()
	manager.versionProbe = func(context.Context) (string, error) {
		return "", errors.New("fresh probe failed")
	}
	manager.mu.Unlock()
	response := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if !response.IsStarted || response.Version != nil {
		t.Fatalf("start response = %#v", response)
	}
	close(releaseStaleProbe)
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.RLock()
		busy := manager.versionProbeBusy
		version := manager.xrayVersion
		manager.mu.RUnlock()
		if !busy {
			if version != nil {
				t.Fatalf("stale probe overwrote fresh start result: %q", *version)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale health probe did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProcessWaitDelayBoundsInheritedLogPipe(t *testing.T) {
	manager, _ := newLifecycleManager(t, "exit-with-stdio-child")
	manager.readinessProbe = func(context.Context) bool { return false }
	started := time.Now()
	response := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if response.IsStarted {
		t.Fatalf("unexpected start response: %#v", response)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("inherited output pipe delayed process reap for %s", elapsed)
	}
}

func TestConcurrentFinalizeRetriesCleanupAndReapsLeaderOnce(t *testing.T) {
	cleanupFailure := errors.New("injected cleanup failure")
	leaderExit := errors.New("injected leader exit")
	var cleanupAttempts atomic.Int32
	var reapCalls atomic.Int32
	reapEntered := make(chan struct{})
	releaseReap := make(chan struct{})
	var reapEnteredOnce sync.Once
	manager := &Manager{
		processGroupCleanup: func(*os.Process, time.Duration) error {
			cleanupAttempts.Add(1)
			return cleanupFailure
		},
	}
	process := &processState{
		cmd: &exec.Cmd{},
		reap: func() error {
			reapCalls.Add(1)
			reapEnteredOnce.Do(func() { close(reapEntered) })
			<-releaseReap
			return leaderExit
		},
		done:           make(chan struct{}),
		leaderDone:     make(chan struct{}),
		monitorDone:    make(chan struct{}),
		stdout:         discardWriteCloser{},
		stderr:         discardWriteCloser{},
		leaderObserved: true,
	}

	if err := manager.finalizeExitedProcess(process, time.Second); !errors.Is(err, cleanupFailure) {
		t.Fatalf("first finalize error = %v", err)
	}
	outcome := process.outcome()
	if outcome.finalized || outcome.cleanupVerified || !errors.Is(outcome.cleanupErr, cleanupFailure) {
		t.Fatalf("outcome after failed cleanup = %#v", outcome)
	}
	if got := reapCalls.Load(); got != 0 {
		t.Fatalf("leader reaped before cleanup verification: calls=%d", got)
	}

	retryEntered := make(chan struct{})
	releaseRetry := make(chan struct{})
	var retryOnce sync.Once
	manager.mu.Lock()
	manager.processGroupCleanup = func(*os.Process, time.Duration) error {
		cleanupAttempts.Add(1)
		retryOnce.Do(func() { close(retryEntered) })
		<-releaseRetry
		return nil
	}
	manager.mu.Unlock()

	results := []chan error{make(chan error, 1), make(chan error, 1)}
	for _, result := range results {
		go func(result chan<- error) {
			result <- manager.finalizeExitedProcess(process, time.Second)
		}(result)
	}
	awaitSignal(t, retryEntered, "cleanup finalizer retry")
	close(releaseRetry)
	awaitSignal(t, reapEntered, "leader reap")
	killResult := make(chan error, 1)
	go func() { killResult <- process.kill() }()
	close(releaseReap)
	for index, result := range results {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("finalizer %d: %v", index+1, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for finalizer %d", index+1)
		}
	}
	select {
	case err := <-killResult:
		if !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("kill racing with leader reap = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("kill did not resume after leader finalization")
	}

	if got := cleanupAttempts.Load(); got != 2 {
		t.Fatalf("cleanup calls = %d, want 2 (one failed, one successful)", got)
	}
	if got := reapCalls.Load(); got != 1 {
		t.Fatalf("leader reap calls = %d, want 1", got)
	}
	outcome = process.outcome()
	if !outcome.finalized || !outcome.cleanupVerified || !errors.Is(outcome.cleanupErr, cleanupFailure) || !errors.Is(outcome.leaderErr, leaderExit) {
		t.Fatalf("final outcome = %#v", outcome)
	}
	select {
	case <-process.done:
	default:
		t.Fatal("finalized process did not close done")
	}
	if err := manager.finalizeExitedProcess(process, time.Second); err != nil {
		t.Fatalf("idempotent finalize: %v", err)
	}
	if got := reapCalls.Load(); got != 1 {
		t.Fatalf("idempotent finalize repeated leader reap: calls=%d", got)
	}
}

func TestConcurrentStartIsRejected(t *testing.T) {
	manager, process := newLifecycleManager(t, "hold")
	probeEntered := make(chan struct{})
	allowReady := make(chan struct{})
	var once sync.Once
	manager.readinessProbe = func(ctx context.Context) bool {
		once.Do(func() { close(probeEntered) })
		select {
		case <-allowReady:
			return true
		case <-ctx.Done():
			return false
		}
	}

	first := make(chan StartResponse, 1)
	go func() { first <- manager.Start(context.Background(), lifecycleStartRequest("client-a")) }()
	awaitSignal(t, probeEntered, "first start")

	second := manager.Start(context.Background(), lifecycleStartRequest("client-b"))
	if second.IsStarted || second.Error == nil || *second.Error != "Request already in progress" {
		t.Fatalf("concurrent start response = %#v", second)
	}
	if got := process.starts.Load(); got != 1 {
		t.Fatalf("spawn count = %d, want 1", got)
	}

	close(allowReady)
	if resp := awaitStartResponse(t, first); !resp.IsStarted {
		t.Fatalf("first start failed: %#v", resp)
	}
}

func TestStopCancelsStart(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	probeEntered := make(chan struct{})
	var once sync.Once
	manager.readinessProbe = func(ctx context.Context) bool {
		once.Do(func() { close(probeEntered) })
		<-ctx.Done()
		return false
	}

	response := make(chan StartResponse, 1)
	go func() { response <- manager.Start(context.Background(), lifecycleStartRequest("client-a")) }()
	awaitSignal(t, probeEntered, "readiness probe")

	if stopped := manager.Stop(); !stopped.IsStopped {
		t.Fatalf("Stop response = %#v", stopped)
	}
	resp := awaitStartResponse(t, response)
	if resp.IsStarted || resp.Error == nil || !strings.Contains(*resp.Error, "canceled") {
		t.Fatalf("start response after stop = %#v", resp)
	}
	assertStoppedAndCleared(t, manager)
}

func TestStartContextCancellationReapsProcess(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	probeEntered := make(chan struct{})
	var once sync.Once
	manager.readinessProbe = func(ctx context.Context) bool {
		once.Do(func() { close(probeEntered) })
		<-ctx.Done()
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	response := make(chan StartResponse, 1)
	go func() { response <- manager.Start(ctx, lifecycleStartRequest("client-a")) }()
	awaitSignal(t, probeEntered, "readiness probe")
	cancel()

	resp := awaitStartResponse(t, response)
	if resp.IsStarted || resp.Error == nil || !strings.Contains(*resp.Error, context.Canceled.Error()) {
		t.Fatalf("canceled start response = %#v", resp)
	}
	assertStoppedAndCleared(t, manager)
}

func TestStartTimeoutReapsProcess(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.startupTimeout = 40 * time.Millisecond
	manager.readinessProbe = func(context.Context) bool { return false }

	resp := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if resp.IsStarted || resp.Error == nil || !strings.Contains(*resp.Error, "did not become reachable within") {
		t.Fatalf("timeout response = %#v", resp)
	}
	assertStoppedAndCleared(t, manager)
}

func TestProcessExitBeforeReadinessIsReported(t *testing.T) {
	manager, _ := newLifecycleManager(t, "exit-immediately")
	manager.readinessProbe = func(context.Context) bool { return false }

	resp := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if resp.IsStarted || resp.Error == nil || !strings.Contains(*resp.Error, "exited before") {
		t.Fatalf("early exit response = %#v", resp)
	}
	assertStoppedAndCleared(t, manager)
}

func TestTailLogFileReadsOnlyBoundedSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xray.err.log")
	prefix := strings.Repeat("old-data-without-newline", 1024)
	want := "last-one | last-two | last-three"
	if err := os.WriteFile(path, []byte(prefix+"\nlast-one\nlast-two\nlast-three\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tailLogFile(path, 3); got != want {
		t.Fatalf("tailLogFile = %q, want %q", got, want)
	}
}

func TestProcessExitAfterReadinessIsNotCommitted(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool {
		manager.mu.RLock()
		process := manager.process
		manager.mu.RUnlock()
		if process == nil || process.cmd.Process == nil {
			return false
		}
		_ = process.cmd.Process.Kill()
		<-process.done
		return true
	}

	resp := manager.Start(context.Background(), lifecycleStartRequest("client-a"))
	if resp.IsStarted || resp.Error == nil || !strings.Contains(*resp.Error, "exited before") {
		t.Fatalf("post-readiness exit response = %#v", resp)
	}
	assertStoppedAndCleared(t, manager)
}

func TestNaturalExitTransitionsRunningProcessToStopped(t *testing.T) {
	manager, _ := newLifecycleManager(t, "exit-after")
	manager.readinessProbe = func(context.Context) bool { return true }

	if resp := manager.Start(context.Background(), lifecycleStartRequest("client-a")); !resp.IsStarted {
		t.Fatalf("start failed: %#v", resp)
	}
	waitForState(t, manager, lifecycleStopped)
	assertStoppedAndCleared(t, manager)
}

func TestStopUsesInterruptBeforeKill(t *testing.T) {
	manager, process := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	if resp := manager.Start(context.Background(), lifecycleStartRequest("client-a")); !resp.IsStarted {
		t.Fatalf("start failed: %#v", resp)
	}
	waitForEvent(t, process.events, "ready")

	if resp := manager.Stop(); !resp.IsStopped {
		t.Fatalf("stop failed: %#v", resp)
	}
	waitForEvent(t, process.events, "interrupt")
	assertStoppedAndCleared(t, manager)
}

func TestStopEscalatesToKill(t *testing.T) {
	manager, process := newLifecycleManager(t, "ignore-interrupt")
	manager.readinessProbe = func(context.Context) bool { return true }
	manager.interruptTimeout = 40 * time.Millisecond
	if resp := manager.Start(context.Background(), lifecycleStartRequest("client-a")); !resp.IsStarted {
		t.Fatalf("start failed: %#v", resp)
	}
	waitForEvent(t, process.events, "ready")

	manager.mu.RLock()
	runningProcess := manager.process
	manager.mu.RUnlock()
	if resp := manager.Stop(); !resp.IsStopped {
		t.Fatalf("stop failed: %#v", resp)
	}
	exited, exitErr := runningProcess.exitStatus()
	if !exited || exitErr == nil || !strings.Contains(exitErr.Error(), "killed") {
		t.Fatalf("expected SIGKILL exit, exited=%v err=%v", exited, exitErr)
	}
	assertStoppedAndCleared(t, manager)
}

func TestRepeatedStopIsIdempotent(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	for attempt := 0; attempt < 2; attempt++ {
		if resp := manager.Stop(); !resp.IsStopped {
			t.Fatalf("Stop attempt %d = %#v", attempt+1, resp)
		}
	}
}

func TestConcurrentStopsJoinSameTransition(t *testing.T) {
	manager, process := newLifecycleManager(t, "ignore-interrupt")
	manager.readinessProbe = func(context.Context) bool { return true }
	manager.interruptTimeout = 100 * time.Millisecond
	if resp := manager.Start(context.Background(), lifecycleStartRequest("client-a")); !resp.IsStarted {
		t.Fatalf("start failed: %#v", resp)
	}
	waitForEvent(t, process.events, "ready")

	first := make(chan StopResponse, 1)
	go func() { first <- manager.Stop() }()
	waitForStopOperation(t, manager)
	second := make(chan StopResponse, 1)
	go func() { second <- manager.Stop() }()

	for index, response := range []<-chan StopResponse{first, second} {
		select {
		case resp := <-response:
			if !resp.IsStopped {
				t.Fatalf("concurrent Stop %d = %#v", index+1, resp)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for concurrent Stop %d", index+1)
		}
	}
	assertStoppedAndCleared(t, manager)
}

func TestUnchangedStartDoesNotRespawnProcess(t *testing.T) {
	manager, process := newLifecycleManager(t, "hold")
	manager.readinessProbe = func(context.Context) bool { return true }
	request := lifecycleStartRequest("client-a")

	if resp := manager.Start(context.Background(), request); !resp.IsStarted {
		t.Fatalf("first start failed: %#v", resp)
	}
	if resp := manager.Start(context.Background(), request); !resp.IsStarted {
		t.Fatalf("unchanged start failed: %#v", resp)
	}
	if got := process.starts.Load(); got != 1 {
		t.Fatalf("spawn count = %d, want 1", got)
	}
}

func TestHealthReadsCachedLifecycleStateWithoutProbe(t *testing.T) {
	manager, _ := newLifecycleManager(t, "hold")
	var probes atomic.Int32
	manager.readinessProbe = func(context.Context) bool {
		probes.Add(1)
		return true
	}

	if health := manager.Health(); health.XrayInternalStatusCached {
		t.Fatalf("stopped manager reported online: %#v", health)
	}
	manager.mu.Lock()
	manager.state = lifecycleRunning
	manager.mu.Unlock()
	if health := manager.Health(); !health.XrayInternalStatusCached {
		t.Fatalf("running manager reported offline: %#v", health)
	}
	if got := probes.Load(); got != 0 {
		t.Fatalf("Health invoked readiness probe %d times", got)
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitStartResponse(t *testing.T, response <-chan StartResponse) StartResponse {
	t.Helper()
	select {
	case resp := <-response:
		return resp
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for start response")
		return StartResponse{}
	}
}

func waitForState(t *testing.T, manager *Manager, want lifecycleState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.RLock()
		state := manager.state
		manager.mu.RUnlock()
		if state == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	manager.mu.RLock()
	got := manager.state
	manager.mu.RUnlock()
	t.Fatalf("state = %s, want %s", got, want)
}

func waitForStopOperation(t *testing.T, manager *Manager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.RLock()
		stopping := manager.state == lifecycleStopping && manager.stopOp != nil
		manager.mu.RUnlock()
		if stopping {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for active stop transition")
}

func waitForEvent(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event %q in %s", want, path)
}

func assertStoppedAndCleared(t *testing.T, manager *Manager) {
	t.Helper()
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.state != lifecycleStopped || manager.process != nil {
		t.Fatalf("manager not stopped: state=%s process=%v", manager.state, manager.process != nil)
	}
	if len(manager.pendingConfigJSON) != 0 || manager.emptyConfigHash != "" || len(manager.inboundHashes) != 0 {
		t.Fatalf("runtime state not cleared: pending=%v empty=%q hashes=%d", len(manager.pendingConfigJSON) != 0, manager.emptyConfigHash, len(manager.inboundHashes))
	}
}
