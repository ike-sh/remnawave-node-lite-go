package plugin

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

const (
	tableName            = "remnanode"
	torrentBlockerSet    = "torrent-blocker"
	ingressFilterIPSet   = "ingress-filter-ip"
	egressFilterIPSet    = "egress-filter-ip"
	egressFilterPortSet  = "egress-filter-port"
)

type TorrentReport struct {
	ActionReport struct {
		Blocked       bool      `json:"blocked"`
		IP            string    `json:"ip"`
		BlockDuration int       `json:"blockDuration"`
		WillUnblockAt time.Time `json:"willUnblockAt"`
		UserID        string    `json:"userId"`
		ProcessedAt   time.Time `json:"processedAt"`
	} `json:"actionReport"`
	XrayReport map[string]any `json:"xrayReport"`
}

type State struct {
	mu sync.RWMutex

	configHash   string
	pluginUUID   string
	pluginName   string
	hasActive    bool
	whitelistIPs map[string]struct{}
	reports      []TorrentReport
	torrent      torrentSettings
}

func NewState() *State {
	return &State{
		whitelistIPs: make(map[string]struct{}),
	}
}

func (s *State) IsWhitelisted(ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.whitelistIPs[ip]
	return ok
}

func (s *State) HasActivePlugin() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasActive
}

func (s *State) ConfigHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.configHash
}

func (s *State) ReportsCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.reports)
}

func (s *State) FlushReports() []TorrentReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.reports
	s.reports = nil
	return out
}

func (s *State) AddReport(report TorrentReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, report)
}

func (s *State) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configHash = ""
	s.pluginUUID = ""
	s.pluginName = ""
	s.hasActive = false
	s.whitelistIPs = make(map[string]struct{})
	s.reports = nil
	s.torrent = torrentSettings{}
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

func (s *State) UpdateFromSync(plugin *SyncPlugin) (changed bool, accepted bool) {
	if plugin == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !s.hasActive {
			return false, false
		}
		s.configHash = ""
		s.pluginUUID = ""
		s.pluginName = ""
		s.hasActive = false
		s.whitelistIPs = make(map[string]struct{})
		s.reports = nil
		s.torrent = torrentSettings{}
		return true, true
	}

	hash := hashPluginConfig(plugin.Config)

	s.mu.Lock()
	defer s.mu.Unlock()
	if hash == s.configHash && s.hasActive {
		return false, true
	}

	s.configHash = hash
	s.hasActive = true
	s.pluginUUID = plugin.UUID
	s.pluginName = plugin.Name

	var rawConfig map[string]any
	if err := json.Unmarshal(plugin.Config, &rawConfig); err != nil {
		rawConfig = nil
	}

	shared := buildSharedIPMap(rawConfig)

	s.whitelistIPs = make(map[string]struct{})
	if connectionDrop, ok := rawConfig["connectionDrop"].(map[string]any); ok {
		if enabled, _ := connectionDrop["enabled"].(bool); enabled {
			for _, ip := range resolveIPList(toStringSlice(connectionDrop["whitelistIps"]), shared) {
				s.whitelistIPs[ip] = struct{}{}
			}
		}
	}
	s.configureTorrentBlocker(rawConfig, shared)

	return true, true
}

func toStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if str, ok := item.(string); ok && str != "" {
			out = append(out, str)
		}
	}
	return out
}

func toIntStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if n, ok := asInt(item); ok {
			out = append(out, fmt.Sprintf("%d", n))
		}
	}
	return out
}
