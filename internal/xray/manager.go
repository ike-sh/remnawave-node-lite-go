package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"remnawave-node-lite-go/internal/system"
	nodeversion "remnawave-node-lite-go/internal/version"
)

type Options struct {
	XrayBin              string
	GeoDir               string
	LogDir               string
	DataDir              string
	InternalSocketPath   string
	InternalRESTToken    string
	XtlsAPIPort          int
	DisableHashCheck     bool
	LowMemory            bool
}

type TorrentBlockerConfigProvider interface {
	TorrentBlockerEnabled() bool
	TorrentBlockerIncludeRuleTags() []string
}

type Manager struct {
	mu               sync.RWMutex
	xrayBin          string
	geoDir           string
	logDir           string
	dataDir          string
	socketPath       string
	token            string
	xtlsAPIPort      int
	disableHashCheck bool
	lowMemory        bool
	internalCerts    internalCerts
	torrentBlocker   TorrentBlockerConfigProvider

	xrayVersion     *string
	xrayOnline      bool
	startProcessing bool
	currentConfig   map[string]any
	emptyConfigHash string
	inboundHashes   map[string]*HashedSet
	inboundTags     map[string]struct{}
	process         *processState
}

type processState struct {
	cmd    *exec.Cmd
	done   chan error
	stdout *os.File
	stderr *os.File

	mu      sync.Mutex
	exited  bool
	exitErr error
}

type StartRequest struct {
	Internals  StartInternals `json:"internals"`
	XrayConfig map[string]any `json:"xrayConfig"`
}

type StartInternals struct {
	ForceRestart bool       `json:"forceRestart"`
	Hashes       ConfigHash `json:"hashes"`
}

type ConfigHash struct {
	EmptyConfig string        `json:"emptyConfig"`
	Inbounds    []InboundHash `json:"inbounds"`
}

type InboundHash struct {
	UsersCount int    `json:"usersCount"`
	Hash       string `json:"hash"`
	Tag        string `json:"tag"`
}

type StartResponse struct {
	IsStarted       bool            `json:"isStarted"`
	Version         *string         `json:"version"`
	Error           *string         `json:"error"`
	NodeInformation NodeInformation `json:"nodeInformation"`
	System          system.Snapshot `json:"system"`
}

type NodeInformation struct {
	Version *string `json:"version"`
}

type StopResponse struct {
	IsStopped bool `json:"isStopped"`
}

type HealthResponse struct {
	IsAlive                  bool    `json:"isAlive"`
	XrayInternalStatusCached bool    `json:"xrayInternalStatusCached"`
	XrayVersion              *string `json:"xrayVersion"`
	NodeVersion              string  `json:"nodeVersion"`
}

func NewManager(opts Options) (*Manager, error) {
	certs, err := generateInternalCerts()
	if err != nil {
		return nil, fmt.Errorf("generate internal Xray certificates: %w", err)
	}
	manager := &Manager{
		xrayBin:          opts.XrayBin,
		geoDir:           opts.GeoDir,
		logDir:           opts.LogDir,
		dataDir:          opts.DataDir,
		socketPath:       opts.InternalSocketPath,
		token:            opts.InternalRESTToken,
		xtlsAPIPort:      opts.XtlsAPIPort,
		disableHashCheck: opts.DisableHashCheck,
		lowMemory:        opts.LowMemory,
		internalCerts:    certs,
	}
	manager.refreshVersion()
	return manager, nil
}

func (m *Manager) SetTorrentBlockerProvider(provider TorrentBlockerConfigProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.torrentBlocker = provider
}

func (m *Manager) torrentBlockerOptions() TorrentBlockerOptions {
	m.mu.RLock()
	socketPath := m.socketPath
	token := m.token
	provider := m.torrentBlocker
	m.mu.RUnlock()

	opts := TorrentBlockerOptions{
		SocketPath: socketPath,
		RESTToken:  token,
	}
	if provider != nil {
		opts.Enabled = provider.TorrentBlockerEnabled()
		opts.IncludeRuleTags = provider.TorrentBlockerIncludeRuleTags()
	}
	return opts
}

func (m *Manager) Start(ctx context.Context, req StartRequest) StartResponse {
	log.Printf("xray/start received (forceRestart=%v)", req.Internals.ForceRestart)

	if err := os.MkdirAll(m.logDir, 0o755); err != nil {
		message := err.Error()
		log.Printf("xray/start failed: %s", message)
		return m.startResponse(false, &message)
	}

	m.mu.Lock()
	if m.startProcessing {
		m.mu.Unlock()
		message := "Request already in progress"
		log.Printf("xray/start rejected: %s", message)
		return m.startResponse(false, &message)
	}
	m.startProcessing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.startProcessing = false
		m.mu.Unlock()
	}()

	fullConfig := generateAPIConfig(req.XrayConfig, m.xtlsAPIPort, m.internalCerts, m.torrentBlockerOptions())

	m.mu.RLock()
	online := m.xrayOnline
	disableCheck := m.disableHashCheck
	forceRestart := req.Internals.ForceRestart
	m.mu.RUnlock()

	if online && !disableCheck && !forceRestart {
		if m.PingXrayGRPC(ctx) {
			m.mu.RLock()
			needRestart := m.isNeedRestartCoreLocked(req.Internals.Hashes)
			m.mu.RUnlock()
			if !needRestart {
				m.mu.Lock()
				m.currentConfig = fullConfig
				m.extractUsersFromConfigLocked(req.Internals.Hashes, fullConfig)
				m.mu.Unlock()
				m.persistStartRequest(req)
				log.Printf("xray/start skipped: core already online and config unchanged")
				return m.startResponse(true, nil)
			}
		} else {
			m.mu.Lock()
			m.xrayOnline = false
			m.mu.Unlock()
		}
	}

	m.mu.Lock()
	m.currentConfig = fullConfig
	m.extractUsersFromConfigLocked(req.Internals.Hashes, fullConfig)
	if err := m.stopProcessLocked(false); err != nil {
		m.mu.Unlock()
		message := err.Error()
		log.Printf("xray/start failed: stop previous rw-core: %s", message)
		return m.startResponse(false, &message)
	}

	process, err := m.startProcessLocked()
	if err != nil {
		m.xrayOnline = false
		m.mu.Unlock()
		message := err.Error()
		log.Printf("xray/start failed: spawn rw-core: %s", message)
		return m.startResponse(false, &message)
	}
	m.process = process
	m.xrayOnline = false
	m.mu.Unlock()

	started := m.waitForGRPC(ctx, m.grpcStartupTimeout())

	m.mu.Lock()
	if started {
		m.xrayOnline = true
		m.mu.Unlock()
		m.persistStartRequest(req)
		log.Printf("xray/start succeeded: rw-core online on gRPC 127.0.0.1:%d", m.xtlsAPIPort)
		return m.startResponse(true, nil)
	}
	_ = m.stopProcessLocked(false)
	m.xrayOnline = false
	m.mu.Unlock()

	message := fmt.Sprintf("xray gRPC API on 127.0.0.1:%d did not become reachable within %s (see %s/xray.err.log)", m.xtlsAPIPort, m.grpcStartupTimeout(), m.logDir)
	if hint := m.rwCoreExitHint(); hint != "" {
		message += "; " + hint
	}
	if tail := tailLogFile(filepath.Join(m.logDir, "xray.err.log"), 3); tail != "" {
		message += "; xray.err: " + tail
	}
	log.Printf("xray/start failed: %s", message)
	return m.startResponse(false, &message)
}

func (m *Manager) grpcStartupTimeout() time.Duration {
	if m.lowMemory {
		return 90 * time.Second
	}
	return 20 * time.Second
}

func (m *Manager) persistStartRequest(req StartRequest) {
	if err := savePersistedStart(m.dataDir, req); err != nil {
		log.Printf("warning: save persisted xray config: %v", err)
		return
	}
	log.Printf("persisted xray config to %s", filepath.Join(m.dataDir, persistedStartFile))
}

// Stop stops rw-core. When clearPersist is true (Panel /node/xray/stop), persisted
// boot config is removed so the node stays disabled after reboot. Process shutdown
// must pass clearPersist=false so RestoreOnBoot can recover rw-core on next start.
func (m *Manager) Stop(clearPersist bool) StopResponse {
	m.mu.Lock()
	err := m.stopProcessLocked(true)
	m.mu.Unlock()
	if clearPersist {
		if clearErr := clearPersistedStart(m.dataDir); clearErr != nil {
			log.Printf("warning: clear persisted xray config: %v", clearErr)
		}
	}
	return StopResponse{IsStopped: err == nil}
}

func (m *Manager) RestoreOnBoot(ctx context.Context) {
	delay := 2 * time.Second
	if m.lowMemory {
		delay = 5 * time.Second
	}
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
	}

	req, err := loadPersistedStart(m.dataDir)
	if err != nil {
		log.Printf("warning: load persisted xray config: %v", err)
		return
	}
	if req == nil {
		log.Printf("no persisted xray config at %s (Panel must xray/start once before reboot auto-restore)", filepath.Join(m.dataDir, persistedStartFile))
		return
	}
	log.Printf("restoring rw-core from persisted config")
	resp := m.Start(ctx, *req)
	if !resp.IsStarted {
		msg := "unknown error"
		if resp.Error != nil {
			msg = *resp.Error
		}
		log.Printf("restore rw-core failed: %s", msg)
	}
}

func (m *Manager) Health() HealthResponse {
	m.refreshVersion()

	m.mu.RLock()
	process := m.process
	cached := m.xrayOnline
	version := m.xrayVersion
	m.mu.RUnlock()

	online := cached
	if process != nil || cached {
		pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		online = m.PingXrayGRPC(pingCtx)
		cancel()
	}

	m.mu.Lock()
	m.xrayOnline = online
	m.mu.Unlock()

	return HealthResponse{
		IsAlive:                  true,
		XrayInternalStatusCached: online,
		XrayVersion:              version,
		NodeVersion:              nodeversion.ReportedNodeVersion(),
	}
}

func (m *Manager) CurrentConfig() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.currentConfig == nil {
		return map[string]any{}
	}
	return cloneMap(m.currentConfig)
}

func (m *Manager) XrayBin() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.xrayBin
}

func (m *Manager) CommandArgs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return BuildCommandArgs(m.socketPath, m.token)
}

func BuildCommandArgs(socketPath, token string) []string {
	return []string{
		"-config",
		BuildConfigURL(socketPath, token),
		"-format",
		"json",
	}
}

func BuildConfigURL(socketPath, token string) string {
	return fmt.Sprintf("http+unix://%s/internal/get-config?token=%s", socketPath, url.QueryEscape(token))
}

func (m *Manager) startProcessLocked() (*processState, error) {
	stdout, err := os.OpenFile(filepath.Join(m.logDir, "xray.out.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open xray stdout log: %w", err)
	}
	stderr, err := os.OpenFile(filepath.Join(m.logDir, "xray.err.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("open xray stderr log: %w", err)
	}

	cmd := exec.Command(m.xrayBin, BuildCommandArgs(m.socketPath, m.token)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+m.geoDir)

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("start rw-core: %w", err)
	}

	process := &processState{
		cmd:    cmd,
		done:   make(chan error, 1),
		stdout: stdout,
		stderr: stderr,
	}
	go m.monitorProcess(process)

	return process, nil
}

func (m *Manager) monitorProcess(process *processState) {
	err := process.cmd.Wait()
	_ = process.stdout.Close()
	_ = process.stderr.Close()
	process.markExited(err)
	if err != nil {
		log.Printf("rw-core exited: %v", err)
	}
	process.done <- err
	close(process.done)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process == process {
		m.process = nil
		m.xrayOnline = false
	}
}

func (p *processState) markExited(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exited = true
	p.exitErr = err
}

func (p *processState) exitStatus() (exited bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exited, p.exitErr
}

func (m *Manager) rwCoreExitHint() string {
	m.mu.RLock()
	process := m.process
	m.mu.RUnlock()
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
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, " | ")
}

func (m *Manager) stopProcessLocked(clearConfig bool) error {
	process := m.process
	if process == nil {
		m.xrayOnline = false
		if clearConfig {
			m.currentConfig = nil
		}
		return nil
	}

	if process.cmd.Process != nil {
		if err := process.cmd.Process.Signal(os.Interrupt); err != nil {
			_ = process.cmd.Process.Kill()
		}
	}

	select {
	case <-process.done:
	case <-time.After(5 * time.Second):
		if process.cmd.Process != nil {
			_ = process.cmd.Process.Kill()
		}
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
			return errors.New("timed out stopping rw-core process")
		}
	}

	m.process = nil
	m.xrayOnline = false
	if clearConfig {
		m.currentConfig = nil
		m.clearHashStateLocked()
		m.clearInboundTagsLocked()
	}
	return nil
}

func (m *Manager) refreshVersion() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, m.xrayBin, "version").Output()
	if err != nil {
		return
	}
	version := parseVersionLine(string(output))
	if version == "" {
		return
	}

	m.mu.Lock()
	m.xrayVersion = &version
	m.mu.Unlock()
}

var xraySemverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// parseVersionLine returns semver like "26.3.27", matching official node (XRAY_CORE_VERSION / semver.coerce).
func parseVersionLine(output string) string {
	if env := strings.TrimSpace(os.Getenv("XRAY_CORE_VERSION")); env != "" {
		if v := coerceSemver(env); v != "" {
			return v
		}
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if v := extractSemver(line); v != "" {
			return v
		}
	}
	return ""
}

func coerceSemver(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "v")
	return extractSemver(raw)
}

func extractSemver(raw string) string {
	if raw == "" {
		return ""
	}
	return xraySemverRe.FindString(raw)
}

func (m *Manager) startResponse(isStarted bool, message *string) StartResponse {
	m.mu.RLock()
	version := m.xrayVersion
	m.mu.RUnlock()

	return StartResponse{
		IsStarted: isStarted,
		Version:   version,
		Error:     message,
		NodeInformation: NodeInformation{
			Version: stringPtr(nodeversion.ReportedNodeVersion()),
		},
		System: system.GetSnapshot(),
	}
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		return map[string]any{}
	}
	return output
}

func stringPtr(value string) *string {
	return &value
}
