package xray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/unixconfig"
)

const (
	defaultReadinessInterval = 2 * time.Second
	defaultInterruptTimeout  = 5 * time.Second
	defaultKillTimeout       = 5 * time.Second
	defaultProcessWaitDelay  = 2 * time.Second
)

var (
	errGRPCStartupTimeout = errors.New("xray gRPC startup timeout")
	errProcessExited      = errors.New("rw-core exited before becoming ready")
)

type lifecycleState uint8

const (
	lifecycleStopped lifecycleState = iota
	lifecycleStarting
	lifecycleRunning
	lifecycleStopping
)

func (s lifecycleState) String() string {
	switch s {
	case lifecycleStopped:
		return "stopped"
	case lifecycleStarting:
		return "starting"
	case lifecycleRunning:
		return "running"
	case lifecycleStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

type stopOperation struct {
	done      chan struct{}
	isStopped bool
}

type processLeaderWait struct {
	reaped    bool
	leaderErr error
}

type processOutcome struct {
	leaderObserved  bool
	finalized       bool
	cleanupVerified bool
	leaderErr       error
	cleanupErr      error
	observationErr  error
}

type processState struct {
	cmd         *exec.Cmd
	reap        func() error
	generation  uint64
	done        chan struct{}
	leaderDone  chan struct{}
	monitorDone chan struct{}
	stdout      io.WriteCloser
	stderr      io.WriteCloser

	finalizeMu      sync.Mutex
	mu              sync.Mutex
	leaderObserved  bool
	leaderReaped    bool
	finalized       bool
	cleanupVerified bool
	leaderErr       error
	cleanupErr      error
	observationErr  error
}

func (m *Manager) Start(parent context.Context, req StartRequest) StartResponse {
	log.Printf("xray/start received (forceRestart=%v)", req.Internals.ForceRestart)

	if err := os.MkdirAll(m.logDir, 0o755); err != nil {
		return m.startFailure("create Xray log directory", err)
	}

	ctx, cancel, generation, previous, ok := m.beginStart(parent)
	if !ok {
		message := "Request already in progress"
		log.Printf("xray/start rejected: %s", message)
		return m.startResponse(false, &message)
	}

	if err := ctx.Err(); err != nil {
		cancel()
		m.completeStart(generation, previous, nil)
		return m.startFailure("xray start canceled", err)
	}

	if previous == lifecycleRunning && !m.disableHashCheck && !req.Internals.ForceRestart {
		if m.probeReadiness(ctx) {
			if err := ctx.Err(); err != nil {
				cancel()
				m.completeStart(generation, previous, nil)
				return m.startFailure("xray start canceled", err)
			}
			m.mu.RLock()
			needRestart := m.isNeedRestartCoreLocked(req.Internals.Hashes)
			m.mu.RUnlock()
			if !needRestart {
				completed, owned := m.completeUnchangedStart(generation)
				if completed {
					cancel()
					log.Printf("xray/start skipped: core already online and config unchanged")
					return m.startResponse(true, nil)
				}
				if !owned {
					cancel()
					return m.startFailure("xray start canceled", context.Canceled)
				}
			}
		}
		if err := ctx.Err(); err != nil {
			cancel()
			m.completeStart(generation, previous, nil)
			return m.startFailure("xray start canceled", err)
		}
	}

	prepared, err := prepareRuntimeConfig(req.XrayConfig, req.Internals.Hashes, m.xtlsSocket, m.torrentBlockerOptions())
	// The prepared value contains only canonical JSON plus compact hash state;
	// release the decoded request tree before waiting for low-memory rw-core.
	req.XrayConfig = nil
	if err != nil {
		cancel()
		m.completeStart(generation, previous, nil)
		return m.startFailure("prepare Xray config", err)
	}
	if err := ctx.Err(); err != nil {
		cancel()
		m.completeStart(generation, previous, nil)
		return m.startFailure("xray start canceled", err)
	}

	m.mu.RLock()
	previousProcess := m.process
	m.mu.RUnlock()
	if err := m.terminateProcess(previousProcess); err != nil {
		cancel()
		m.completeStart(generation, lifecycleStopping, nil)
		return m.startFailure("stop previous rw-core", err)
	}

	if err := ctx.Err(); err != nil {
		cancel()
		m.completeStart(generation, lifecycleStopped, func() {
			m.process = nil
			m.clearRuntimeLocked()
		})
		return m.startFailure("xray start canceled", err)
	}

	if !m.stagePendingConfig(generation, previousProcess, prepared.json) {
		cancel()
		m.completeStart(generation, lifecycleStopped, nil)
		return m.startFailure("xray start canceled", context.Canceled)
	}

	process, err := m.startProcess(generation)
	if err != nil {
		cancel()
		m.completeStart(generation, lifecycleStopped, m.clearRuntimeLocked)
		return m.startFailure("spawn rw-core", err)
	}

	if !m.assignProcess(generation, process) {
		stopErr := m.terminateProcess(process)
		finalState := lifecycleStopped
		if stopErr != nil {
			finalState = lifecycleStopping
			m.retainUncleanProcess(process)
		}
		cancel()
		m.completeStart(generation, finalState, nil)
		return m.startFailure("xray start canceled", errors.Join(context.Canceled, stopErr))
	}

	startupTimeout := m.grpcStartupTimeout()
	readyErr := m.waitForGRPC(ctx, process, startupTimeout)

	if readyErr == nil {
		version := m.probeVersion(ctx)
		if err := ctx.Err(); err != nil {
			readyErr = err
		} else {
			committed, owned, exitErr := m.commitRunningStart(generation, process, prepared.hashState, version)
			if committed {
				cancel()
				log.Printf("xray/start succeeded: rw-core online on gRPC @%s", m.xtlsSocket)
				return m.startResponse(true, nil)
			}
			if !owned {
				cancel()
				return m.startFailure("xray start canceled", context.Canceled)
			}
			readyErr = processExitedError(exitErr)
		}
	}

	stopErr := m.terminateProcess(process)
	finalState := lifecycleStopped
	cleanup := func() {
		m.process = nil
		m.clearRuntimeLocked()
	}
	if stopErr != nil {
		finalState = lifecycleStopping
		cleanup = nil
	}
	m.completeStart(generation, finalState, cleanup)
	cancel()

	message := m.readinessFailureMessage(readyErr, process, startupTimeout)
	if stopErr != nil {
		message += "; stop rw-core: " + stopErr.Error()
	}
	if tail := tailLogFile(filepath.Join(m.logDir, "xray.err.log"), 3); tail != "" {
		message += "; xray.err: " + tail
	}
	log.Printf("xray/start failed: %s", message)
	return m.startResponse(false, &message)
}

func (m *Manager) beginStart(parent context.Context) (context.Context, context.CancelFunc, uint64, lifecycleState, bool) {
	if parent == nil {
		parent = context.Background()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == lifecycleStarting || m.state == lifecycleStopping {
		return nil, nil, 0, m.state, false
	}
	if m.process != nil {
		outcome := m.process.outcome()
		if outcome.cleanupErr != nil || outcome.observationErr != nil {
			return nil, nil, 0, m.state, false
		}
	}
	if !m.lifecycleMu.TryLock() {
		return nil, nil, 0, m.state, false
	}

	previous := m.state
	m.generation++
	ctx, cancel := context.WithCancel(parent)
	m.state = lifecycleStarting
	m.startCancel = cancel
	return ctx, cancel, m.generation, previous, true
}

// completeStart publishes the final state and releases lifecycle ownership as
// one atomic action with respect to new Start and Stop calls.
func (m *Manager) completeStart(generation uint64, finalState lifecycleState, apply func()) bool {
	m.mu.Lock()
	owned := m.generation == generation
	if owned {
		if apply != nil {
			apply()
		}
		m.state = finalState
		m.startCancel = nil
	}
	m.lifecycleMu.Unlock()
	m.mu.Unlock()
	return owned
}

// completeUnchangedStart keeps the existing process and config. The process
// liveness check and state publication share the manager lock, so an exit
// callback cannot be lost between the two operations.
func (m *Manager) completeUnchangedStart(generation uint64) (completed, owned bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation {
		m.lifecycleMu.Unlock()
		return false, false
	}
	if m.process == nil {
		return false, true
	}
	if unavailable, _ := m.process.leaderUnavailable(); unavailable {
		return false, true
	}

	m.state = lifecycleRunning
	m.startCancel = nil
	m.lifecycleMu.Unlock()
	return true, true
}

func (m *Manager) stagePendingConfig(generation uint64, previous *processState, raw []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation || m.state != lifecycleStarting {
		return false
	}
	if m.process == previous {
		m.process = nil
	}
	m.clearRuntimeLocked()
	m.pendingConfigJSON = raw
	return true
}

func (m *Manager) assignProcess(generation uint64, process *processState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation || m.state != lifecycleStarting {
		return false
	}
	m.process = process
	return true
}

func (m *Manager) retainUncleanProcess(process *processState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process == nil {
		m.process = process
	}
	m.state = lifecycleStopping
}

func (m *Manager) commitRunningStart(generation uint64, process *processState, hashState runtimeHashState, version *string) (committed, owned bool, exitErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation || m.state != lifecycleStarting {
		m.lifecycleMu.Unlock()
		return false, false, nil
	}
	if m.process != process {
		return false, true, nil
	}
	if unavailable, err := process.leaderUnavailable(); unavailable {
		return false, true, err
	}

	m.pendingConfigJSON = nil
	m.applyRuntimeHashStateLocked(hashState)
	m.publishVersionLocked(version)
	m.state = lifecycleRunning
	m.startCancel = nil
	m.lifecycleMu.Unlock()
	return true, true, nil
}

func (m *Manager) Stop() StopResponse {
	op, cancelStart, waitOnly, waitForOwner, retryCleanup := m.reserveStop()
	if waitOnly {
		<-op.done
		return StopResponse{IsStopped: op.isStopped}
	}
	if cancelStart != nil {
		cancelStart()
	}
	if waitForOwner {
		m.lifecycleMu.Lock()
	}

	m.mu.RLock()
	process := m.process
	m.mu.RUnlock()
	var err error
	if retryCleanup {
		err = m.retryProcessCleanup(process)
	} else {
		err = m.terminateProcess(process)
	}
	succeeded := process == nil
	if process != nil {
		outcome := process.outcome()
		succeeded = outcome.finalized && outcome.cleanupVerified
	}

	m.mu.Lock()
	if succeeded {
		if m.process == process {
			m.process = nil
		}
		m.clearRuntimeLocked()
		m.state = lifecycleStopped
	} else {
		m.state = lifecycleStopping
	}
	op.isStopped = succeeded
	m.stopOp = nil
	close(op.done)
	m.lifecycleMu.Unlock()
	m.mu.Unlock()

	if err != nil {
		log.Printf("xray/stop failed: %v", err)
	}
	return StopResponse{IsStopped: succeeded}
}

func (m *Manager) reserveStop() (op *stopOperation, cancelStart context.CancelFunc, waitOnly, waitForOwner, retryCleanup bool) {
	m.mu.Lock()
	if m.state == lifecycleStopping && m.stopOp != nil {
		op = m.stopOp
		m.mu.Unlock()
		return op, nil, true, false, false
	}

	op = &stopOperation{done: make(chan struct{})}
	retryCleanup = m.state == lifecycleStopping
	if m.state == lifecycleStarting {
		m.generation++
		m.state = lifecycleStopping
		m.stopOp = op
		cancelStart = m.startCancel
		m.startCancel = nil
		m.mu.Unlock()
		return op, cancelStart, false, true, false
	}

	if !m.lifecycleMu.TryLock() {
		// State and lifecycle ownership are normally published together. Keep
		// the defensive path cancelable in case a future caller violates it.
		m.generation++
		m.state = lifecycleStopping
		m.stopOp = op
		cancelStart = m.startCancel
		m.startCancel = nil
		m.mu.Unlock()
		return op, cancelStart, false, true, retryCleanup
	}

	m.generation++
	m.state = lifecycleStopping
	m.stopOp = op
	m.mu.Unlock()
	return op, nil, false, false, retryCleanup
}

func (m *Manager) probeReadiness(ctx context.Context) bool {
	m.mu.RLock()
	probe := m.readinessProbe
	m.mu.RUnlock()
	if probe != nil {
		return probe(ctx)
	}
	return m.PingXrayGRPC(ctx)
}

func (m *Manager) waitForGRPC(parent context.Context, process *processState, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	m.mu.RLock()
	interval := m.readinessInterval
	m.mu.RUnlock()
	if interval <= 0 {
		interval = defaultReadinessInterval
	}

	for {
		if exited, err := process.leaderUnavailable(); exited {
			return processExitedError(err)
		}
		if m.probeReadiness(ctx) {
			if err := parent.Err(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return errGRPCStartupTimeout
			}
			if exited, err := process.leaderUnavailable(); exited {
				return processExitedError(err)
			}
			return nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-parent.Done():
			timer.Stop()
			return parent.Err()
		case <-ctx.Done():
			timer.Stop()
			if err := parent.Err(); err != nil {
				return err
			}
			return errGRPCStartupTimeout
		case <-process.leaderDone:
			timer.Stop()
			_, err := process.leaderUnavailable()
			return processExitedError(err)
		case <-timer.C:
		}
	}
}

func processExitedError(err error) error {
	if err == nil {
		return errProcessExited
	}
	return fmt.Errorf("%w: %v", errProcessExited, err)
}

func (m *Manager) grpcStartupTimeout() time.Duration {
	m.mu.RLock()
	configured := m.startupTimeout
	lowMemory := m.lowMemory
	m.mu.RUnlock()
	if configured > 0 {
		return configured
	}
	if lowMemory {
		return 90 * time.Second
	}
	return 20 * time.Second
}

func (m *Manager) readinessFailureMessage(err error, process *processState, timeout time.Duration) string {
	var message string
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		message = "xray start canceled: " + err.Error()
	case errors.Is(err, errProcessExited):
		message = "rw-core exited before the Xray gRPC API became ready"
	case errors.Is(err, errGRPCStartupTimeout):
		message = fmt.Sprintf("xray gRPC API on @%s did not become reachable within %s (see %s/xray.err.log)", m.xtlsSocket, timeout, m.logDir)
	default:
		message = "xray start failed: " + err.Error()
	}
	if hint := processExitHint(process); hint != "" && !strings.Contains(message, hint) {
		message += "; " + hint
	}
	return message
}

func (m *Manager) startFailure(action string, err error) StartResponse {
	message := action
	if err != nil {
		message += ": " + err.Error()
	}
	log.Printf("xray/start failed: %s", message)
	return m.startResponse(false, &message)
}

func (m *Manager) startProcess(generation uint64) (*processState, error) {
	stdout, err := openCappedLogWriter(filepath.Join(m.logDir, "xray.out.log"), maxLogSize)
	if err != nil {
		return nil, fmt.Errorf("open xray stdout log: %w", err)
	}
	stderr, err := openCappedLogWriter(filepath.Join(m.logDir, "xray.err.log"), maxLogSize)
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("open xray stderr log: %w", err)
	}

	m.mu.RLock()
	commandFactory := m.processCommand
	processWaitDelay := m.processWaitDelay
	xrayBin := m.xrayBin
	socketPath := m.socketPath
	geoDir := m.geoDir
	token := m.token
	m.mu.RUnlock()

	var cmd *exec.Cmd
	if commandFactory != nil {
		cmd = commandFactory()
	} else {
		cmd = exec.Command(xrayBin, BuildCommandArgs(socketPath)...)
	}
	if cmd == nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, errors.New("create rw-core command: command factory returned nil")
	}
	if processWaitDelay <= 0 {
		processWaitDelay = defaultProcessWaitDelay
	}
	if cmd.WaitDelay <= 0 {
		cmd.WaitDelay = processWaitDelay
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureProcessOwnership(cmd)
	baseEnv := cmd.Env
	if len(baseEnv) == 0 {
		baseEnv = os.Environ()
	}
	cmd.Env = append(append([]string(nil), baseEnv...),
		"XRAY_LOCATION_ASSET="+geoDir,
		unixconfig.InternalTokenEnvVar+"="+token,
	)

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start rw-core: %w", err)
	}

	process := &processState{
		cmd:         cmd,
		reap:        cmd.Wait,
		generation:  generation,
		done:        make(chan struct{}),
		leaderDone:  make(chan struct{}),
		monitorDone: make(chan struct{}),
		stdout:      stdout,
		stderr:      stderr,
	}
	go m.monitorProcess(process)
	return process, nil
}

func (m *Manager) monitorProcess(process *processState) {
	leader, observationErr := waitForProcessLeader(process)
	process.markLeaderWait(leader, observationErr)
	var cleanupErr error
	if observationErr == nil {
		cleanupErr = m.finalizeExitedProcess(process, m.processCleanupTimeout())
	}
	close(process.monitorDone)

	outcome := process.outcome()
	if outcome.observationErr != nil {
		log.Printf("rw-core leader observation failed (generation=%d): %v", process.generation, outcome.observationErr)
	}
	if cleanupErr != nil {
		log.Printf("rw-core process-group cleanup failed (generation=%d): %v", process.generation, cleanupErr)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process != process {
		return
	}
	switch m.state {
	case lifecycleRunning:
		m.clearRuntimeLocked()
		if outcome.finalized {
			m.process = nil
			m.state = lifecycleStopped
		} else {
			m.state = lifecycleStopping
		}
	case lifecycleStopping:
		if m.stopOp == nil {
			m.clearRuntimeLocked()
			if outcome.finalized {
				m.process = nil
				m.state = lifecycleStopped
			}
		}
	}
}

func (p *processState) markLeaderWait(result processLeaderWait, observationErr error) {
	p.mu.Lock()
	p.leaderObserved = observationErr == nil
	p.leaderReaped = result.reaped
	p.leaderErr = result.leaderErr
	p.observationErr = observationErr
	p.mu.Unlock()
	close(p.leaderDone)
}

func (p *processState) exitStatus() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.finalized, p.leaderErr
}

func (p *processState) leaderUnavailable() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.observationErr != nil {
		return true, p.observationErr
	}
	return p.leaderObserved || p.finalized, p.leaderErr
}

func (p *processState) outcome() processOutcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	return processOutcome{
		leaderObserved:  p.leaderObserved,
		finalized:       p.finalized,
		cleanupVerified: p.cleanupVerified,
		leaderErr:       p.leaderErr,
		cleanupErr:      p.cleanupErr,
		observationErr:  p.observationErr,
	}
}

func (p *processState) markCleanupFailure(err error) {
	p.mu.Lock()
	p.cleanupErr = err
	p.mu.Unlock()
}

func (p *processState) markFinalized(leaderErr error) {
	p.mu.Lock()
	p.finalized = true
	p.cleanupVerified = true
	p.leaderErr = leaderErr
	p.mu.Unlock()
}

func (p *processState) signal(signal os.Signal) error {
	p.finalizeMu.Lock()
	defer p.finalizeMu.Unlock()
	if outcome := p.outcome(); outcome.finalized {
		return os.ErrProcessDone
	}
	return signalOwnedProcess(p.cmd.Process, signal)
}

func (p *processState) kill() error {
	p.finalizeMu.Lock()
	defer p.finalizeMu.Unlock()
	if outcome := p.outcome(); outcome.finalized {
		return os.ErrProcessDone
	}
	return killOwnedProcess(p.cmd.Process)
}

func (m *Manager) processCleanupTimeout() time.Duration {
	m.mu.RLock()
	timeout := m.killTimeout
	m.mu.RUnlock()
	if timeout <= 0 {
		return defaultKillTimeout
	}
	return timeout
}

func (m *Manager) finalizeExitedProcess(process *processState, timeout time.Duration) error {
	process.finalizeMu.Lock()
	defer process.finalizeMu.Unlock()

	outcome := process.outcome()
	if outcome.finalized {
		return nil
	}
	if outcome.observationErr != nil {
		return fmt.Errorf("observe rw-core leader exit: %w", outcome.observationErr)
	}
	if !outcome.leaderObserved {
		return errors.New("rw-core leader exit has not been observed")
	}

	process.mu.Lock()
	leaderReaped := process.leaderReaped
	process.mu.Unlock()
	leaderErr := outcome.leaderErr
	if !leaderReaped {
		m.mu.RLock()
		cleanup := m.processGroupCleanup
		m.mu.RUnlock()
		if cleanup == nil {
			cleanup = cleanupOwnedProcessGroup
		}
		if err := cleanup(process.cmd.Process, timeout); err != nil {
			cleanupErr := fmt.Errorf("cleanup rw-core process group: %w", err)
			process.markCleanupFailure(cleanupErr)
			return cleanupErr
		}
		leaderErr = process.reap()
	}

	_ = process.stdout.Close()
	_ = process.stderr.Close()
	process.markFinalized(leaderErr)
	close(process.done)
	if leaderErr != nil {
		log.Printf("rw-core leader exited (generation=%d): %v", process.generation, leaderErr)
	}
	return nil
}

func (m *Manager) terminateProcess(process *processState) error {
	if process == nil {
		return nil
	}
	if exited, _ := process.exitStatus(); exited {
		return nil
	}

	m.mu.RLock()
	interruptTimeout := m.interruptTimeout
	killTimeout := m.killTimeout
	m.mu.RUnlock()
	if interruptTimeout <= 0 {
		interruptTimeout = defaultInterruptTimeout
	}
	if killTimeout <= 0 {
		killTimeout = defaultKillTimeout
	}

	if process.cmd.Process != nil {
		err := process.signal(os.Interrupt)
		if err == nil || errors.Is(err, os.ErrProcessDone) {
			if waitForProcessAttempt(process, interruptTimeout) {
				return processTerminationResult(process)
			}
		}
		if err := process.kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill rw-core: %w", err)
		}
	}
	if waitForProcessAttempt(process, killTimeout) {
		return processTerminationResult(process)
	}
	if err := processTerminationFailure(process); err != nil {
		return err
	}
	return errors.New("timed out stopping rw-core process")
}

func (m *Manager) retryProcessCleanup(process *processState) error {
	if process == nil {
		return nil
	}
	outcome := process.outcome()
	if outcome.finalized {
		return nil
	}
	if outcome.observationErr != nil {
		return fmt.Errorf("observe rw-core leader exit: %w", outcome.observationErr)
	}
	if outcome.leaderObserved && outcome.cleanupErr != nil {
		return m.finalizeExitedProcess(process, m.processCleanupTimeout())
	}
	return m.terminateProcess(process)
}

func waitForProcessAttempt(process *processState, timeout time.Duration) bool {
	if exited, _ := process.exitStatus(); exited {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-process.done:
		return true
	case <-process.monitorDone:
		return true
	case <-timer.C:
		return false
	}
}

func processTerminationResult(process *processState) error {
	outcome := process.outcome()
	if outcome.finalized {
		return nil
	}
	if err := processTerminationFailure(process); err != nil {
		return err
	}
	return errors.New("rw-core process termination did not finalize")
}

func processTerminationFailure(process *processState) error {
	outcome := process.outcome()
	if outcome.observationErr != nil {
		return fmt.Errorf("observe rw-core leader exit: %w", outcome.observationErr)
	}
	if outcome.cleanupErr != nil {
		return outcome.cleanupErr
	}
	return nil
}

func processExitHint(process *processState) string {
	if process == nil {
		return "rw-core is not running"
	}
	exited, err := process.exitStatus()
	if !exited {
		return ""
	}
	if err != nil {
		return "rw-core exited: " + err.Error()
	}
	return "rw-core exited"
}

func tailLogFile(path string, maxLines int) string {
	const maxTailBytes int64 = 8 << 10
	if maxLines <= 0 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return ""
	}
	start := info.Size() - maxTailBytes
	if start < 0 {
		start = 0
	}
	data := make([]byte, info.Size()-start)
	n, err := file.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	data = data[:n]
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, " | ")
}
