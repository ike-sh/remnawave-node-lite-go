package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
)

type XrayController interface {
	StopIfOnline() bool
	RemoveTorrentBlockerOutbound() error
}

type Service struct {
	state   *State
	nft     *nftManager
	dropper *connections.Dropper
	xray    XrayController
}

func NewService(state *State, dropper *connections.Dropper, xray XrayController) *Service {
	return &Service{
		state:   state,
		nft:     newNFTManager(),
		dropper: dropper,
		xray:    xray,
	}
}

type AcceptedResponse struct {
	Accepted bool `json:"accepted"`
}

type CollectReportsResponse struct {
	Reports []TorrentReport `json:"reports"`
}

type BlockIP struct {
	IP      string
	Timeout float64
}

func (s *Service) Sync(request *SyncPlugin) AcceptedResponse {
	if request == nil {
		return s.clearPlugin()
	}
	if isUnchangedPluginConfig(request, s.state) {
		return AcceptedResponse{Accepted: true}
	}

	rawConfig := extractPluginConfig(request)
	if err := ValidatePluginConfig(rawConfig); err != nil {
		slog.Warn("plugin config validation failed", "error", err)
		s.ResetPlugins()
		if s.xray != nil {
			s.xray.StopIfOnline()
		}
		return AcceptedResponse{Accepted: false}
	}

	wasEnabled := s.state.TorrentBlockerEnabled()
	prevIncludeTags := append([]string(nil), s.state.TorrentBlockerIncludeRuleTags()...)
	changed, accepted := s.state.UpdateFromSync(request)
	if !accepted {
		return AcceptedResponse{Accepted: false}
	}

	nowEnabled := s.state.TorrentBlockerEnabled()
	nowIncludeTags := s.state.TorrentBlockerIncludeRuleTags()
	if changed && s.nft.Available() {
		_ = s.nft.recreateTables()
		if rawConfig != nil {
			s.syncFilters(rawConfig)
		}
	}

	s.applyTorrentRestart(wasEnabled, nowEnabled, prevIncludeTags, nowIncludeTags)
	return AcceptedResponse{Accepted: true}
}

func (s *Service) clearPlugin() AcceptedResponse {
	if !s.state.HasActivePlugin() {
		return AcceptedResponse{Accepted: false}
	}
	slog.Info("plugin sync received empty payload, cleaning up active plugin")
	s.ResetPlugins()
	if s.xray != nil {
		s.xray.StopIfOnline()
	}
	return AcceptedResponse{Accepted: true}
}

// ResetPlugins clears plugin state and nftables plugin tables (official withPluginCleanup).
func (s *Service) ResetPlugins() {
	s.state.Reset()
	if s.nft.Available() {
		_ = s.nft.recreateTables()
	}
}

func (s *Service) applyTorrentRestart(wasEnabled, nowEnabled bool, prevIncludeTags, nowIncludeTags []string) {
	if s.xray == nil {
		return
	}
	switch {
	case wasEnabled && !nowEnabled && len(nowIncludeTags) == 0:
		_ = s.xray.RemoveTorrentBlockerOutbound()
	default:
		needsRestart := (wasEnabled && !nowEnabled) ||
			(!wasEnabled && nowEnabled) ||
			(wasEnabled && nowEnabled && hashIncludeRuleTags(prevIncludeTags) != hashIncludeRuleTags(nowIncludeTags))
		if needsRestart {
			s.xray.StopIfOnline()
		}
	}
}

func (s *Service) CollectReports() CollectReportsResponse {
	reports := s.state.FlushReports()
	if reports == nil {
		reports = []TorrentReport{}
	}
	return CollectReportsResponse{Reports: reports}
}

func (s *Service) BlockIPs(items []BlockIP) AcceptedResponse {
	if !s.nft.Available() {
		return AcceptedResponse{Accepted: false}
	}
	for _, item := range items {
		if err := s.nft.blockIP(item.IP, item.Timeout); err != nil {
			return AcceptedResponse{Accepted: false}
		}
		if s.dropper != nil {
			s.dropper.DropIPs([]string{item.IP})
		}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) UnblockIPs(ips []string) AcceptedResponse {
	if !s.nft.Available() {
		return AcceptedResponse{Accepted: false}
	}
	for _, ip := range ips {
		if err := s.nft.unblockIP(ip); err != nil {
			return AcceptedResponse{Accepted: false}
		}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) RecreateTables() AcceptedResponse {
	if !s.nft.Available() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.recreateTables(); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) ReportsCount() int {
	return s.state.ReportsCount()
}

func hashIncludeRuleTags(tags []string) string {
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	if len(sorted) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(sum[:])
}
