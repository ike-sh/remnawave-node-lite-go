package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	InternalSocketPath   string
	InternalRESTToken    string
	XtlsAPIPort          int
	DisableHashCheck     bool
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
	socketPath       string
	token            string
	xtlsAPIPort      int
	disableHashCheck bool
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
		socketPath:       opts.InternalSocketPath,
		token:            opts.InternalRESTToken,
		xtlsAPIPort:      opts.XtlsAPIPort,
		disableHashCheck: opts.DisableHashCheck,
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
	if err := os.MkdirAll(m.logDir, 0o755); err != nil {
		message := err.Error()
		return m.startResponse(false, &message)
	}

	m.mu.Lock()
	if m.startProcessing {
		m.mu.Unlock()
		message := "Request already in progress"
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
		return m.startResponse(false, &message)
	}

	process, err := m.startProcessLocked()
	if err != nil {
		m.xrayOnline = false
		m.mu.Unlock()
		message := err.Error()
		return m.startResponse(false, &message)
	}
	m.process = process
	m.xrayOnline = false
	m.mu.Unlock()

	started := m.waitForGRPC(ctx, 20*time.Second)

	m.mu.Lock()
	if started {
		m.xrayOnline = true
		m.mu.Unlock()
		return m.startResponse(true, nil)
	}
	_ = m.stopProcessLocked(false)
	m.xrayOnline = false
	m.mu.Unlock()

	message := fmt.Sprintf("xray gRPC API on 127.0.0.1:%d did not become reachable within 20s", m.xtlsAPIPort)
	return m.startResponse(false, &message)
}

func (m *Manager) Stop() StopResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.stopProcessLocked(true)
	return StopResponse{IsStopped: err == nil}
}

func (m *Manager) Health() HealthResponse {
	m.refreshVersion()

	m.mu.RLock()
	defer m.mu.RUnlock()

	return HealthResponse{
		IsAlive:                  true,
		XrayInternalStatusCached: m.xrayOnline,
		XrayVersion:              m.xrayVersion,
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
	process.done <- err
	close(process.done)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process == process {
		m.process = nil
		m.xrayOnline = false
	}
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

func parseVersionLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
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
