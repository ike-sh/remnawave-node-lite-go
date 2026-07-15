package plugin

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xraywebhook"
)

const (
	tableName           = "remnanode"
	torrentBlockerSet   = "torrent-blocker"
	ingressFilterIPSet  = "ingress-filter-ip"
	egressFilterIPSet   = "egress-filter-ip"
	egressFilterPortSet = "egress-filter-port"
	maxTorrentReports   = 1024
)

type TorrentReport struct {
	ActionReport struct {
		Blocked       bool      `json:"blocked"`
		IP            string    `json:"ip"`
		BlockDuration float64   `json:"blockDuration"`
		WillUnblockAt time.Time `json:"willUnblockAt"`
		UserID        string    `json:"userId"`
		ProcessedAt   time.Time `json:"processedAt"`
	} `json:"actionReport"`
	XrayReport xraywebhook.Payload `json:"xrayReport"`
}

// pluginSnapshot is immutable after publication. Readers may retain its
// pointer after releasing State.mu because every update publishes a new value.
type pluginSnapshot struct {
	configHash   string
	pluginUUID   string
	pluginName   string
	whitelistIPs map[string]struct{}
	torrent      torrentSettings
	firewall     firewallConfig
}

type State struct {
	mu sync.RWMutex

	active         *pluginSnapshot
	reports        []TorrentReport
	reportHead     int
	droppedReports uint64
	asn            ASNResolver
}

func NewState() *State {
	return &State{}
}

// SetASNResolver installs the resolver used to expand asList shared lists into
// CIDR prefixes. Call once during startup before requests are served.
func (s *State) SetASNResolver(r ASNResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asn = r
}

func (s *State) asnResolver() ASNResolver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.asn
}

func (s *State) currentSnapshot() *pluginSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

func (s *State) commitSnapshot(snapshot *pluginSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = snapshot
}

func (s *State) IsWhitelisted(ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil {
		return false
	}
	_, ok := s.active.whitelistIPs[ip]
	return ok
}

func (s *State) HasActivePlugin() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active != nil
}

func (s *State) ConfigHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil {
		return ""
	}
	return s.active.configHash
}

func (s *State) ReportsCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.reports)
}

func (s *State) FlushReports() ([]TorrentReport, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.reports
	if s.reportHead != 0 {
		out = make([]TorrentReport, 0, len(s.reports))
		out = append(out, s.reports[s.reportHead:]...)
		out = append(out, s.reports[:s.reportHead]...)
	}
	dropped := s.droppedReports
	s.reports = nil
	s.reportHead = 0
	s.droppedReports = 0
	return out, dropped
}

func (s *State) AddReport(report TorrentReport) (dropped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reports) == maxTorrentReports {
		s.reports[s.reportHead] = report
		s.reportHead = (s.reportHead + 1) % maxTorrentReports
		s.droppedReports++
		return true
	}
	s.reports = append(s.reports, report)
	return false
}

type SyncPlugin struct {
	UUID   string          `json:"uuid"`
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

// NewSyncPlugin builds a sync payload preserving JSON key order for config hashing.
func NewSyncPlugin(uuid, name string, config map[string]any) (*SyncPlugin, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	return &SyncPlugin{UUID: uuid, Name: name, Config: raw}, nil
}

// NewSyncPluginFromEnvelope parses the plugin envelope used in tests and HTTP sync bodies.
func NewSyncPluginFromEnvelope(raw map[string]any) (*SyncPlugin, error) {
	uuid, _ := raw["uuid"].(string)
	name, _ := raw["name"].(string)
	config, _ := raw["config"].(map[string]any)
	return NewSyncPlugin(uuid, name, config)
}
