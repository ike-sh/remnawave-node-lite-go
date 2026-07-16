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
	"sync/atomic"
	"time"

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
	operationGate chan struct{}

	state       *State
	nft         firewallBackend
	dropper     *connections.Dropper
	xray        XrayController
	initialized bool
	closed      bool
	// closeDone means backend and snapshot cleanup completed. The webhook
	// worker may still be finishing, so callers must also wait on webhookDone.
	closeDone bool
	// closing is a one-way admission fence. Operations accepted before it is
	// set may finish; all later mutations are rejected while Close may retry.
	closing atomic.Bool

	webhookQueue       chan queuedWebhook
	webhookStop        chan struct{}
	webhookDone        chan struct{}
	webhookContext     context.Context
	webhookCancel      context.CancelFunc
	webhookStopOnce    sync.Once
	webhookAdmissionMu sync.RWMutex
	webhookStopped     atomic.Bool
	webhookDropped     atomic.Uint64
	cleanupTimeout     time.Duration
}

const (
	pluginCleanupTimeout = 15 * time.Second
	maxQueuedWebhooks    = 64
)

func NewService(state *State, dropper *connections.Dropper, xray XrayController) *Service {
	return newServiceWithBackend(state, dropper, xray, newNFTManager())
}

func newServiceWithBackend(state *State, dropper *connections.Dropper, xray XrayController, nft firewallBackend) *Service {
	if state == nil {
		state = NewState()
	}
	webhookContext, webhookCancel := context.WithCancel(context.Background())
	service := &Service{
		operationGate:  make(chan struct{}, 1),
		state:          state,
		nft:            nft,
		dropper:        dropper,
		xray:           xray,
		webhookQueue:   make(chan queuedWebhook, maxQueuedWebhooks),
		webhookStop:    make(chan struct{}),
		webhookDone:    make(chan struct{}),
		webhookContext: webhookContext,
		webhookCancel:  webhookCancel,
		cleanupTimeout: pluginCleanupTimeout,
	}
	go service.runWebhookWorker()
	return service
}

// Initialize explicitly probes and creates this process's nftables tables.
// An unavailable backend is a supported degraded mode, so callers may log the
// returned error and continue serving connectionDrop and non-plugin routes.
func (s *Service) Initialize() error {
	return s.InitializeContext(context.Background())
}

func (s *Service) InitializeContext(ctx context.Context) error {
	if err := s.acquireMutation(ctx); err != nil {
		return err
	}
	defer s.releaseOperation()
	if s.closed {
		return errPluginServiceClosed
	}
	if s.initialized && s.nft != nil && s.nft.Available() {
		return nil
	}
	if s.nft == nil {
		s.initialized = true
		return errNFTablesUnavailable
	}
	if err := s.nft.Initialize(ctx); err != nil {
		if contextError(ctx) != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		s.initialized = true
		return err
	}
	s.initialized = true
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
	return s.SyncContext(context.Background(), request)
}

func (s *Service) SyncContext(ctx context.Context, request *SyncPlugin) AcceptedResponse {
	if err := s.acquireMutation(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	defer s.releaseOperation()
	if err := s.readyLocked(); err != nil {
		slog.Warn("plugin sync rejected", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	if request == nil {
		return s.clearPluginLocked(ctx)
	}

	config, err := decodePluginConfig(request)
	if err != nil {
		slog.Warn("plugin config validation failed", "error", err)
		if cleanupErr := s.applySnapshotLocked(ctx, nil, s.stopXrayLocked); cleanupErr != nil {
			slog.Warn("failed to clean up invalid plugin config", "error", cleanupErr)
		}
		return AcceptedResponse{Accepted: false}
	}

	previous := s.state.currentSnapshot()
	firewallReady := s.firewallAvailableLocked()
	sourceHash := sha256.Sum256(request.Config)
	if previous != nil && previous.configHash != "" && previous.sourceHash == sourceHash &&
		previous.pluginUUID == request.UUID && previous.pluginName == request.Name &&
		previous.firewallReady == firewallReady {
		return AcceptedResponse{Accepted: true}
	}

	plan := buildPluginPlanFromConfig(request, config, s.state.asnResolver(), firewallReady)
	plan.logDiagnostics()

	if err := s.applySnapshotLocked(ctx, plan.snapshot, func(previous, next *pluginSnapshot) error {
		return s.reconcileTorrentLocked(previous, next, plan)
	}); err != nil {
		slog.Warn("plugin sync failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) clearPluginLocked(ctx context.Context) AcceptedResponse {
	if s.state.currentSnapshot() == nil {
		return AcceptedResponse{Accepted: false}
	}
	slog.Info("plugin sync received empty payload, cleaning up active plugin")
	if err := s.applySnapshotLocked(ctx, nil, s.stopXrayLocked); err != nil {
		slog.Warn("plugin cleanup failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

// ResetPlugins clears committed plugin state and rules. The caller owns Xray
// shutdown ordering and must confirm rw-core stopped before removing rules.
func (s *Service) ResetPlugins() error {
	return s.ResetPluginsContext(context.Background())
}

func (s *Service) ResetPluginsContext(ctx context.Context) error {
	if err := s.acquireMutation(ctx); err != nil {
		return err
	}
	defer s.releaseOperation()
	if err := s.readyLocked(); err != nil {
		return err
	}
	return s.applySnapshotLocked(ctx, nil, nil)
}

func (s *Service) applySnapshotLocked(ctx context.Context, next *pluginSnapshot, reconcile func(previous, next *pluginSnapshot) error) error {
	previous := s.state.currentSnapshot()
	previousFirewall := snapshotFirewall(previous)
	nextFirewall := snapshotFirewall(next)

	firewallApplied := false
	if s.firewallAvailableLocked() {
		if err := s.nft.Apply(ctx, nextFirewall); err != nil {
			rollbackErr := s.restoreFirewallLocked(previousFirewall)
			return errors.Join(
				fmt.Errorf("apply plugin firewall plan: %w", err),
				wrapFirewallRollbackError(rollbackErr),
			)
		}
		firewallApplied = true
	}
	if err := contextError(ctx); err != nil {
		if firewallApplied {
			rollbackErr := s.restoreFirewallLocked(previousFirewall)
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if reconcile != nil {
		if err := reconcile(previous, next); err != nil {
			if firewallApplied {
				rollbackErr := s.restoreFirewallLocked(previousFirewall)
				if rollbackErr != nil {
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

func (s *Service) restoreFirewallLocked(config firewallConfig) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), s.effectiveCleanupTimeout())
	defer cancel()
	return s.nft.Apply(rollbackCtx, config)
}

func wrapFirewallRollbackError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restore previous firewall plan: %w", err)
}

func snapshotFirewall(snapshot *pluginSnapshot) firewallConfig {
	if snapshot == nil || !snapshot.firewallReady {
		return firewallConfig{}
	}
	return snapshot.firewall.clone()
}

func (s *Service) reconcileTorrentLocked(previous, next *pluginSnapshot, plan *pluginPlan) error {
	if s.xray == nil {
		return nil
	}

	wasEnabled := effectiveTorrentEnabled(previous)
	nowEnabled := effectiveTorrentEnabled(next)
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
	reports, dropped := s.state.FlushReports()
	if dropped != 0 {
		slog.Warn("torrent report queue overflowed before collection", "dropped", dropped, "retained", len(reports))
	}
	if reports == nil {
		reports = []TorrentReport{}
	}
	return CollectReportsResponse{Reports: reports}
}

func (s *Service) BlockIPs(items []BlockIP) AcceptedResponse {
	return s.BlockIPsContext(context.Background(), items)
}

func (s *Service) BlockIPsContext(ctx context.Context, items []BlockIP) AcceptedResponse {
	if err := s.acquireMutation(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	defer s.releaseOperation()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.BlockIPs(ctx, items); err != nil {
		slog.Warn("nftables block request failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	if s.dropper != nil {
		ips := make([]string, 0, len(items))
		for _, item := range items {
			ips = append(ips, item.IP)
		}
		s.dropper.DropIPs(ctx, ips)
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) UnblockIPs(ips []string) AcceptedResponse {
	return s.UnblockIPsContext(context.Background(), ips)
}

func (s *Service) UnblockIPsContext(ctx context.Context, ips []string) AcceptedResponse {
	if err := s.acquireMutation(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	defer s.releaseOperation()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.UnblockIPs(ctx, ips); err != nil {
		slog.Warn("nftables unblock request failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) RecreateTables() AcceptedResponse {
	return s.RecreateTablesContext(context.Background())
}

func (s *Service) RecreateTablesContext(ctx context.Context) AcceptedResponse {
	if err := s.acquireMutation(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	defer s.releaseOperation()
	if s.readyLocked() != nil || s.nft == nil {
		return AcceptedResponse{Accepted: false}
	}
	if !s.nft.Available() {
		if err := s.nft.Initialize(ctx); err != nil {
			slog.Warn("nftables recovery initialization failed", "error", err)
			return AcceptedResponse{Accepted: false}
		}
	}

	previous := s.state.currentSnapshot()
	next := previous
	if previous != nil && !previous.firewallReady {
		copy := *previous
		copy.firewallReady = true
		next = &copy
	}
	if err := s.applySnapshotLocked(ctx, next, func(previous, next *pluginSnapshot) error {
		return s.reconcileTorrentLocked(previous, next, &pluginPlan{})
	}); err != nil {
		slog.Warn("nftables recreate request failed", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func effectiveTorrentEnabled(snapshot *pluginSnapshot) bool {
	return snapshot != nil && snapshot.firewallReady && snapshot.torrent.enabled
}

func (s *Service) ReportsCount() int {
	return s.state.ReportsCount()
}

// Close prevents new plugin mutations and removes only this process's tables.
// Once closing begins the service never reopens. An admitted mutation may
// finish, and failed cleanup retains its snapshot for a later Close retry.
func (s *Service) Close() error {
	return s.CloseContext(context.Background())
}

// CloseContext applies the caller's shutdown budget to gate admission, backend
// cleanup, and worker joining while preserving Close's retry semantics.
func (s *Service) CloseContext(parent context.Context) error {
	if parent == nil {
		parent = context.Background()
	}
	cleanupCtx, cancel := context.WithTimeout(parent, s.effectiveCleanupTimeout())
	defer cancel()
	s.closing.Store(true)
	s.signalWebhookStop()
	if !s.acquireOperation(cleanupCtx) {
		return operationContextError(cleanupCtx)
	}
	if s.closeDone {
		s.releaseOperation()
		return s.finishWebhookShutdown(cleanupCtx)
	}
	s.closed = true
	if !s.initialized || s.nft == nil {
		s.state.commitSnapshot(nil)
		s.closeDone = true
		s.releaseOperation()
		return s.finishWebhookShutdown(cleanupCtx)
	}
	err := s.nft.Close(cleanupCtx)
	if err != nil {
		s.releaseOperation()
		return errors.Join(err, s.finishWebhookShutdown(cleanupCtx))
	}
	s.state.commitSnapshot(nil)
	s.closeDone = true
	s.releaseOperation()
	return s.finishWebhookShutdown(cleanupCtx)
}

func (s *Service) acquireMutation(ctx context.Context) error {
	// Check on both sides of the gate: Close can begin while this caller waits.
	if s.closing.Load() {
		return errPluginServiceClosed
	}
	if !s.acquireOperation(ctx) {
		return operationContextError(ctx)
	}
	if s.closing.Load() {
		s.releaseOperation()
		return errPluginServiceClosed
	}
	return nil
}

func (s *Service) acquireOperation(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.operationGate <- struct{}{}:
		if ctx.Err() != nil {
			s.releaseOperation()
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (s *Service) releaseOperation() {
	<-s.operationGate
}

func (s *Service) effectiveCleanupTimeout() time.Duration {
	if s.cleanupTimeout > 0 {
		return s.cleanupTimeout
	}
	return pluginCleanupTimeout
}

func operationContextError(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return context.Canceled
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
