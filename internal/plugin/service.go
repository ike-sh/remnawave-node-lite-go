package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"remnawave-node-lite-go/internal/connections"
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

type envelope[T any] struct {
	Response T `json:"response"`
}

type writeJSONFn func(w http.ResponseWriter, status int, value any)

func (s *Service) HandleSync(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		Plugin *SyncPlugin `json:"plugin"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	if req.Plugin == nil {
		s.handlePluginClear(write, w)
		return
	}

	if isUnchangedPluginConfig(req.Plugin, s.state) {
		writeAccepted(write, w, true)
		return
	}

	rawConfig := extractPluginConfig(req.Plugin)
	if err := ValidatePluginConfig(rawConfig); err != nil {
		slog.Warn("plugin config validation failed", "error", err)
		s.resetPlugins()
		if s.xray != nil {
			s.xray.StopIfOnline()
		}
		writeAccepted(write, w, false)
		return
	}

	wasEnabled := s.state.TorrentBlockerEnabled()
	prevIncludeTags := append([]string(nil), s.state.TorrentBlockerIncludeRuleTags()...)

	changed, accepted := s.state.UpdateFromSync(req.Plugin)
	if !accepted {
		writeAccepted(write, w, false)
		return
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
	writeAccepted(write, w, true)
}

func (s *Service) handlePluginClear(write writeJSONFn, w http.ResponseWriter) {
	if !s.state.HasActivePlugin() {
		writeAccepted(write, w, false)
		return
	}
	slog.Info("plugin sync received empty payload, cleaning up active plugin")
	s.resetPlugins()
	if s.xray != nil {
		s.xray.StopIfOnline()
	}
	writeAccepted(write, w, true)
}

func (s *Service) resetPlugins() {
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

func (s *Service) HandleCollectReports(w http.ResponseWriter, write writeJSONFn) {
	reports := s.state.FlushReports()
	if reports == nil {
		reports = []TorrentReport{}
	}
	write(w, http.StatusOK, envelope[struct {
		Reports []TorrentReport `json:"reports"`
	}]{Response: struct {
		Reports []TorrentReport `json:"reports"`
	}{Reports: reports}})
}

func (s *Service) HandleBlockIPs(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		IPs []struct {
			IP      string `json:"ip"`
			Timeout int    `json:"timeout"`
		} `json:"ips"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	if !s.nft.Available() {
		writeAccepted(write, w, false)
		return
	}

	for _, item := range req.IPs {
		if err := s.nft.blockIP(item.IP, item.Timeout); err != nil {
			writeAccepted(write, w, false)
			return
		}
		if s.dropper != nil {
			s.dropper.DropIPs([]string{item.IP})
		}
	}
	writeAccepted(write, w, true)
}

func (s *Service) HandleUnblockIPs(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}
	if !s.nft.Available() {
		writeAccepted(write, w, false)
		return
	}
	for _, ip := range req.IPs {
		if err := s.nft.unblockIP(ip); err != nil {
			writeAccepted(write, w, false)
			return
		}
	}
	writeAccepted(write, w, true)
}

func (s *Service) HandleRecreateTables(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	if !s.nft.Available() {
		writeAccepted(write, w, false)
		return
	}
	if err := s.nft.recreateTables(); err != nil {
		writeAccepted(write, w, false)
		return
	}
	writeAccepted(write, w, true)
}

func (s *Service) ReportsCount() int {
	return s.state.ReportsCount()
}

func writeAccepted(write writeJSONFn, w http.ResponseWriter, accepted bool) {
	write(w, http.StatusOK, envelope[struct {
		Accepted bool `json:"accepted"`
	}]{Response: struct {
		Accepted bool `json:"accepted"`
	}{Accepted: accepted}})
}

func decodeBody(r *http.Request, target any) bool {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target) == nil
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

func writeError(write writeJSONFn, w http.ResponseWriter, message string) {
	write(w, http.StatusBadRequest, map[string]any{"message": message})
}
