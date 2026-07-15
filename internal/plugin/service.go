package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
)

var (
	errPluginServiceNotInitialized = errors.New("plugin service is not initialized")
	errPluginServiceClosed         = errors.New("plugin service is closed")
)

type XrayController interface {
	StopIfOnline() error
	RemoveTorrentBlockerOutbound() error
}

type Service struct {
	opMu sync.Mutex

	state       *State
	nft         firewallBackend
	dropper     *connections.Dropper
	xray        XrayController
	initialized bool
	closed      bool
	closeDone   bool
}

func NewService(state *State, dropper *connections.Dropper, xray XrayController) *Service {
	return newServiceWithBackend(state, dropper, xray, newNFTManager())
}

func newServiceWithBackend(state *State, dropper *connections.Dropper, xray XrayController, nft firewallBackend) *Service {
	if state == nil {
		state = NewState()
	}
	return &Service{
		state:   state,
		nft:     nft,
		dropper: dropper,
		xray:    xray,
	}
}

// Initialize explicitly probes and creates this process's nftables tables.
// An unavailable backend is a supported degraded mode, so callers may log the
// returned error and continue serving connectionDrop and non-plugin routes.
func (s *Service) Initialize() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closed {
		return errPluginServiceClosed
	}
	if s.initialized {
		return nil
	}
	s.initialized = true
	if s.nft == nil {
		return errNFTablesUnavailable
	}
	if err := s.nft.Initialize(); err != nil {
		return err
	}
	return nil
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
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.readyLocked(); err != nil {
		slog.Warn("plugin sync rejected", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	if request == nil {
		return s.clearPluginLocked()
	}

	previous := s.state.currentSnapshot()
	configHash := hashPluginConfig(request.Config)
	if previous != nil && configHash != "" && configHash == previous.configHash {
		return AcceptedResponse{Accepted: true}
	}

	plan, err := buildPluginPlan(request, s.state.asnResolver(), s.firewallAvailableLocked())
	if err != nil {
		slog.Warn("plugin config validation failed", "error", err)
		if cleanupErr := s.applySnapshotLocked(nil, s.stopXrayLocked); cleanupErr != nil {
			slog.Warn("failed to clean up invalid plugin config", "error", cleanupErr)
		}
		return AcceptedResponse{Accepted: false}
	}
	plan.logDiagnostics()

	if err := s.applySnapshotLocked(plan.snapshot, func(previous, next *pluginSnapshot) error {
		return s.reconcileTorrentLocked(previous, next, plan)
	}); err != nil {
		slog.Warn("plugin sync failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) clearPluginLocked() AcceptedResponse {
	if s.state.currentSnapshot() == nil {
		return AcceptedResponse{Accepted: false}
	}
	slog.Info("plugin sync received empty payload, cleaning up active plugin")
	if err := s.applySnapshotLocked(nil, s.stopXrayLocked); err != nil {
		slog.Warn("plugin cleanup failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

// ResetPlugins clears committed plugin state and rules. The caller owns Xray
// shutdown ordering (the official xray/stop route resets plugins first).
func (s *Service) ResetPlugins() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := s.readyLocked(); err != nil {
		return err
	}
	return s.applySnapshotLocked(nil, nil)
}

func (s *Service) applySnapshotLocked(next *pluginSnapshot, reconcile func(previous, next *pluginSnapshot) error) error {
	previous := s.state.currentSnapshot()
	previousFirewall := snapshotFirewall(previous)
	nextFirewall := snapshotFirewall(next)

	firewallApplied := false
	if s.firewallAvailableLocked() {
		if err := s.nft.Apply(nextFirewall); err != nil {
			return fmt.Errorf("apply plugin firewall plan: %w", err)
		}
		firewallApplied = true
	}

	if reconcile != nil {
		if err := reconcile(previous, next); err != nil {
			if firewallApplied {
				if rollbackErr := s.nft.Apply(previousFirewall); rollbackErr != nil {
					return errors.Join(
						fmt.Errorf("reconcile plugin Xray state: %w", err),
						fmt.Errorf("restore previous firewall plan: %w", rollbackErr),
					)
				}
			}
			return fmt.Errorf("reconcile plugin Xray state: %w", err)
		}
	}

	s.state.commitSnapshot(next)
	return nil
}

func snapshotFirewall(snapshot *pluginSnapshot) firewallConfig {
	if snapshot == nil {
		return firewallConfig{}
	}
	return snapshot.firewall.clone()
}

func (s *Service) reconcileTorrentLocked(previous, next *pluginSnapshot, plan *pluginPlan) error {
	if s.xray == nil {
		return nil
	}

	wasEnabled := previous != nil && previous.torrent.enabled
	nowEnabled := next != nil && next.torrent.enabled
	var previousTags, nextTags []string
	if previous != nil {
		previousTags = previous.torrent.includeRuleTags
	}
	if next != nil {
		nextTags = next.torrent.includeRuleTags
	}

	if wasEnabled && !nowEnabled && !plan.torrentIncludeRuleTagsPresent {
		return s.xray.RemoveTorrentBlockerOutbound()
	}
	needsRestart := (wasEnabled && !nowEnabled) ||
		(!wasEnabled && nowEnabled) ||
		(wasEnabled && nowEnabled && hashIncludeRuleTags(previousTags) != hashIncludeRuleTags(nextTags))
	if needsRestart {
		return s.xray.StopIfOnline()
	}
	return nil
}

func (s *Service) stopXrayLocked(_, _ *pluginSnapshot) error {
	if s.xray == nil {
		return nil
	}
	return s.xray.StopIfOnline()
}

func (s *Service) CollectReports() CollectReportsResponse {
	reports := s.state.FlushReports()
	if reports == nil {
		reports = []TorrentReport{}
	}
	return CollectReportsResponse{Reports: reports}
}

func (s *Service) BlockIPs(items []BlockIP) AcceptedResponse {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.BlockIPs(items); err != nil {
		slog.Warn("nftables block request failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	if s.dropper != nil {
		ips := make([]string, 0, len(items))
		for _, item := range items {
			ips = append(ips, item.IP)
		}
		s.dropper.DropIPs(context.Background(), ips)
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) UnblockIPs(ips []string) AcceptedResponse {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.UnblockIPs(ips); err != nil {
		slog.Warn("nftables unblock request failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) RecreateTables() AcceptedResponse {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.Apply(snapshotFirewall(s.state.currentSnapshot())); err != nil {
		slog.Warn("nftables recreate request failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) ReportsCount() int {
	return s.state.ReportsCount()
}

// Close prevents new plugin mutations and removes only this process's tables.
func (s *Service) Close() error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.closeDone {
		return nil
	}
	s.closed = true
	if !s.initialized || s.nft == nil {
		s.closeDone = true
		return nil
	}
	if err := s.nft.Close(); err != nil {
		return err
	}
	s.closeDone = true
	return nil
}

func (s *Service) readyLocked() error {
	if s.closed {
		return errPluginServiceClosed
	}
	if !s.initialized {
		return errPluginServiceNotInitialized
	}
	return nil
}

func (s *Service) firewallAvailableLocked() bool {
	return s.nft != nil && s.nft.Available()
}

func (p *pluginPlan) logDiagnostics() {
	if p == nil {
		return
	}
	if p.diagnostics.firewallUnavailable {
		slog.Warn("nftables unavailable; nft-dependent plugins remain disabled")
	}
	if p.diagnostics.asnUnavailable {
		slog.Warn("ASN database unavailable; asList entries resolved empty")
	}
	if values := p.diagnostics.missingASNValues(); len(values) != 0 {
		slog.Warn("ASN prefixes not found", "asns", values)
	}
	if values := p.diagnostics.missingSharedListValues(); len(values) != 0 {
		slog.Warn("plugin shared lists not found", "lists", values)
	}
}

func hashIncludeRuleTags(tags []string) string {
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	if len(sorted) == 0 {
		return ""
	}
	raw, _ := json.Marshal(sorted)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
