package xray

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/system"
	nodeversion "github.com/Luxiaba/remnawave-node-lite-go/internal/version"
)

type Options struct {
	XrayBin            string
	GeoDir             string
	LogDir             string
	InternalSocketPath string
	InternalRESTToken  string
	DisableHashCheck   bool
	LowMemory          bool
}

type TorrentBlockerConfigProvider interface {
	TorrentBlockerEnabled() bool
	TorrentBlockerIncludeRuleTags() []string
}

type Manager struct {
	// lifecycleMu serializes process ownership. State publication and
	// lifecycleMu acquisition/release are performed while mu is held.
	lifecycleMu      sync.Mutex
	mu               sync.RWMutex
	xrayBin          string
	geoDir           string
	logDir           string
	socketPath       string
	token            string
	xtlsSocket       string
	disableHashCheck bool
	lowMemory        bool
	torrentBlocker   TorrentBlockerConfigProvider

	xrayVersion *string
	state       lifecycleState
	generation  uint64
	startCancel context.CancelFunc
	stopOp      *stopOperation
	process     *processState

	// pendingConfig is visible to a starting rw-core through the internal
	// socket. It is promoted to activeConfig only after the gRPC API is ready.
	pendingConfig     map[string]any
	pendingConfigJSON []byte
	activeConfig      map[string]any
	activeConfigJSON  []byte
	emptyConfigHash   string
	inboundHashes     map[string]*HashedSet
	inboundTags       map[string]struct{}

	readinessProbe    func(context.Context) bool
	readinessInterval time.Duration
	startupTimeout    time.Duration
	interruptTimeout  time.Duration
	killTimeout       time.Duration
	processCommand    func() *exec.Cmd
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
	UsersCount float64 `json:"usersCount"`
	Hash       string  `json:"hash"`
	Tag        string  `json:"tag"`
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
	socket, err := generateXtlsSocketName()
	if err != nil {
		return nil, fmt.Errorf("generate xtls api socket name: %w", err)
	}
	manager := &Manager{
		xrayBin:           opts.XrayBin,
		geoDir:            opts.GeoDir,
		logDir:            opts.LogDir,
		socketPath:        opts.InternalSocketPath,
		token:             opts.InternalRESTToken,
		xtlsSocket:        socket,
		disableHashCheck:  opts.DisableHashCheck,
		lowMemory:         opts.LowMemory,
		readinessInterval: defaultReadinessInterval,
		interruptTimeout:  defaultInterruptTimeout,
		killTimeout:       defaultKillTimeout,
	}
	manager.refreshVersion(context.Background())
	return manager, nil
}

// generateXtlsSocketName returns a process-unique abstract socket name for the
// Xray gRPC API. The random suffix mirrors upstream 2.8.0 and avoids collisions
// when several nodes share a host.
func generateXtlsSocketName() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "remnanode-xtls-" + hex.EncodeToString(buf), nil
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

func (m *Manager) Health() HealthResponse {
	m.mu.RLock()
	running := m.state == lifecycleRunning
	version := m.xrayVersion
	m.mu.RUnlock()

	return HealthResponse{
		IsAlive:                  true,
		XrayInternalStatusCached: running,
		XrayVersion:              version,
		NodeVersion:              nodeversion.ReportedNodeVersion(),
	}
}

func (m *Manager) CurrentConfig() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config := m.servedConfigLocked()
	if config == nil {
		return map[string]any{}
	}
	return cloneMap(config)
}

// CurrentConfigJSON returns the config exactly as served to rw-core,
// serialized once per xray/start instead of on every get-config poll.
// Callers must treat the returned slice as read-only.
func (m *Manager) CurrentConfigJSON() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()

	config := m.servedConfigJSONLocked()
	if len(config) == 0 {
		return []byte("{}")
	}
	return config
}

func (m *Manager) servedConfigLocked() map[string]any {
	if m.pendingConfig != nil {
		return m.pendingConfig
	}
	return m.activeConfig
}

func (m *Manager) servedConfigJSONLocked() []byte {
	if len(m.pendingConfigJSON) != 0 {
		return m.pendingConfigJSON
	}
	return m.activeConfigJSON
}

func (m *Manager) clearRuntimeLocked() {
	m.pendingConfig = nil
	m.pendingConfigJSON = nil
	m.activeConfig = nil
	m.activeConfigJSON = nil
	m.clearHashStateLocked()
	m.clearInboundTagsLocked()
}

func marshalConfigJSON(config map[string]any) []byte {
	raw, err := json.Marshal(config)
	if err != nil {
		log.Printf("warning: marshal xray config: %v", err)
		return nil
	}
	return raw
}

func (m *Manager) XrayBin() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.xrayBin
}

func (m *Manager) CommandArgs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return BuildCommandArgs(m.socketPath)
}

func BuildCommandArgs(socketPath string) []string {
	return []string{
		"-config",
		BuildConfigURL(socketPath),
		"-format",
		"json",
	}
}

func BuildConfigURL(socketPath string) string {
	return fmt.Sprintf("http+unix://%s/internal/get-config", socketPath)
}

func (m *Manager) refreshVersion(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
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
