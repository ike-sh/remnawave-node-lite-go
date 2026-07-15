package xray

import (
	"context"
	"errors"
	"fmt"
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

type processState struct {
	cmd        *exec.Cmd
	generation uint64
	done       chan struct{}
	stdout     *os.File
	stderr     *os.File

	mu      sync.Mutex
	exited  bool
	exitErr error
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

	fullConfig := generateAPIConfig(req.XrayConfig, m.xtlsSocket, m.torrentBlockerOptions())
	fullConfigJSON := marshalConfigJSON(fullConfig)
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

	if !m.stagePendingConfig(generation, previousProcess, fullConfig, fullConfigJSON) {
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
		_ = m.terminateProcess(process)
		cancel()
		m.completeStart(generation, lifecycleStopped, nil)
		return m.startFailure("xray start canceled", context.Canceled)
	}

	startupTimeout := m.grpcStartupTimeout()
	readyErr := m.waitForGRPC(ctx, process, startupTimeout)

	if readyErr == nil {
		committed, owned, exitErr := m.commitRunningStart(generation, process, req.Internals.Hashes)
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
	if exited, _ := m.process.exitStatus(); exited {
		return false, true
	}

	m.state = lifecycleRunning
	m.startCancel = nil
	m.lifecycleMu.Unlock()
	return true, true
}

func (m *Manager) stagePendingConfig(generation uint64, previous *processState, config map[string]any, raw []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation || m.state != lifecycleStarting {
		return false
	}
	if m.process == previous {
		m.process = nil
	}
	m.clearRuntimeLocked()
	m.pendingConfig = config
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

func (m *Manager) commitRunningStart(generation uint64, process *processState, hashes ConfigHash) (committed, owned bool, exitErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.generation != generation || m.state != lifecycleStarting {
		m.lifecycleMu.Unlock()
		return false, false, nil
	}
	if m.process != process {
		return false, true, nil
	}
	if exited, err := process.exitStatus(); exited {
		return false, true, err
	}

	m.activeConfig = m.pendingConfig
	m.activeConfigJSON = m.pendingConfigJSON
	m.pendingConfig = nil
	m.pendingConfigJSON = nil
	m.extractUsersFromConfigLocked(hashes, m.activeConfig)
	m.state = lifecycleRunning
	m.startCancel = nil
	m.lifecycleMu.Unlock()
	return true, true, nil
}

func (m *Manager) Stop() StopResponse {
	op, cancelStart, waitOnly, waitForOwner := m.reserveStop()
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
	err := m.terminateProcess(process)
	exited := process == nil
	if process != nil {
		exited, _ = process.exitStatus()
	}
	succeeded := exited

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

func (m *Manager) reserveStop() (op *stopOperation, cancelStart context.CancelFunc, waitOnly, waitForOwner bool) {
	m.mu.Lock()
	if m.state == lifecycleStopping && m.stopOp != nil {
		op = m.stopOp
		m.mu.Unlock()
		return op, nil, true, false
	}

	op = &stopOperation{done: make(chan struct{})}
	if m.state == lifecycleStarting {
		m.generation++
		m.state = lifecycleStopping
		m.stopOp = op
		cancelStart = m.startCancel
		m.startCancel = nil
		m.mu.Unlock()
		return op, cancelStart, false, true
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
		return op, cancelStart, false, true
	}

	m.generation++
	m.state = lifecycleStopping
	m.stopOp = op
	m.mu.Unlock()
	return op, nil, false, false
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
		if exited, err := process.exitStatus(); exited {
			return processExitedError(err)
		}
		if m.probeReadiness(ctx) {
			if err := parent.Err(); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return errGRPCStartupTimeout
			}
			if exited, err := process.exitStatus(); exited {
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
		case <-process.done:
			timer.Stop()
			_, err := process.exitStatus()
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
	m.rotateLogs()
	stdout, err := os.OpenFile(filepath.Join(m.logDir, "xray.out.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open xray stdout log: %w", err)
	}
	stderr, err := os.OpenFile(filepath.Join(m.logDir, "xray.err.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("open xray stderr log: %w", err)
	}

	m.mu.RLock()
	commandFactory := m.processCommand
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
		cmd:        cmd,
		generation: generation,
		done:       make(chan struct{}),
		stdout:     stdout,
		stderr:     stderr,
	}
	go m.monitorProcess(process)
	return process, nil
}

func (m *Manager) monitorProcess(process *processState) {
	err := process.cmd.Wait()
	_ = process.stdout.Close()
	_ = process.stderr.Close()
	process.markExited(err)
	close(process.done)
	if err != nil {
		log.Printf("rw-core exited (generation=%d): %v", process.generation, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process != process {
		return
	}
	switch m.state {
	case lifecycleRunning:
		m.process = nil
		m.clearRuntimeLocked()
		m.state = lifecycleStopped
	case lifecycleStopping:
		if m.stopOp == nil {
			m.process = nil
			m.clearRuntimeLocked()
			m.state = lifecycleStopped
		}
	}
}

func (p *processState) markExited(err error) {
	p.mu.Lock()
	p.exited = true
	p.exitErr = err
	p.mu.Unlock()
}

func (p *processState) exitStatus() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited, p.exitErr
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
		err := process.cmd.Process.Signal(os.Interrupt)
		if err == nil || errors.Is(err, os.ErrProcessDone) {
			if waitForProcess(process, interruptTimeout) {
				return nil
			}
		}
		if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill rw-core: %w", err)
		}
	}
	if waitForProcess(process, killTimeout) {
		return nil
	}
	return errors.New("timed out stopping rw-core process")
}

func waitForProcess(process *processState, timeout time.Duration) bool {
	if exited, _ := process.exitStatus(); exited {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-process.done:
		return true
	case <-timer.C:
		return false
	}
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
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, " | ")
}
