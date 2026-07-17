package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
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
	IP string
	// Timeout zero is the explicit block API's bounded-set permanent mode.
	// Automatic torrent blocking requires a positive duration during config validation.
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

	previous := s.state.currentSnapshot()
	firewallReady := s.firewallAvailableLocked()
	if len(request.Config) <= maxPluginConfigBytes {
		sourceHash := sha256.Sum256(request.Config)
		if previous != nil && previous.sourceHash == sourceHash && previous.firewallReady == firewallReady {
			if err := contextError(ctx); err != nil {
				return AcceptedResponse{Accepted: false}
			}
			next := *previous
			next.pluginUUID = request.UUID
			next.pluginName = request.Name
			s.state.commitSnapshot(&next)
			return AcceptedResponse{Accepted: true}
		}
	}
	plan, err := buildPluginPlanContext(ctx, request, s.state.asnResolver(), firewallReady)
	if err != nil {
		if contextError(ctx) != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return AcceptedResponse{Accepted: false}
		}
		slog.Warn("plugin config validation failed", "error", err)
		cleanupErr := s.applySnapshotReconcileFirstLocked(ctx, nil, s.stopXrayLocked)
		if cleanupErr != nil {
			slog.Warn("failed to clean up invalid plugin config", "error", cleanupErr)
		}
		return AcceptedResponse{Accepted: false}
	}
	if err := contextError(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}

	if pluginSnapshotsBehaviorEqual(previous, plan.snapshot) {
		// Metadata and object-hash changes do not justify rebuilding nftables.
		// Publishing the newly built immutable snapshot preserves identity and
		// config-provider behavior while retaining dynamic torrent blocks.
		s.state.commitSnapshot(plan.snapshot)
		return AcceptedResponse{Accepted: true}
	}

	plan.logDiagnostics()

	reconcile := func(previous, next *pluginSnapshot) error {
		return s.reconcileTorrentLocked(previous, next)
	}
	var applyErr error
	if effectiveTorrentEnabled(previous) && !effectiveTorrentEnabled(plan.snapshot) {
		applyErr = s.applySnapshotReconcileFirstLocked(ctx, plan.snapshot, reconcile)
	} else {
		applyErr = s.applySnapshotLocked(ctx, plan.snapshot, reconcile)
	}
	if applyErr != nil {
		slog.Warn("plugin sync failed", "error", applyErr)
		return AcceptedResponse{Accepted: false}
	}
	return AcceptedResponse{Accepted: true}
}

func (s *Service) clearPluginLocked(ctx context.Context) AcceptedResponse {
	if s.state.currentSnapshot() == nil {
		return AcceptedResponse{Accepted: false}
	}
	slog.Info("plugin sync received empty payload, cleaning up active plugin")
	if err := s.applySnapshotReconcileFirstLocked(ctx, nil, s.stopXrayLocked); err != nil {
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
	return s.applySnapshotWithOrderLocked(ctx, next, reconcile, false, false)
}

func (s *Service) applySnapshotReconcileFirstLocked(ctx context.Context, next *pluginSnapshot, reconcile func(previous, next *pluginSnapshot) error) error {
	return s.applySnapshotWithOrderLocked(ctx, next, reconcile, true, false)
}

func (s *Service) applySnapshotResetReconcileFirstLocked(ctx context.Context, next *pluginSnapshot, reconcile func(previous, next *pluginSnapshot) error) error {
	return s.applySnapshotWithOrderLocked(ctx, next, reconcile, true, true)
}

func (s *Service) applySnapshotWithOrderLocked(
	ctx context.Context,
	next *pluginSnapshot,
	reconcile func(previous, next *pluginSnapshot) error,
	reconcileFirst bool,
	forceReset bool,
) error {
	previous := s.state.currentSnapshot()
	previousFirewall := snapshotFirewall(previous)
	nextFirewall := snapshotFirewall(next)

	if err := contextError(ctx); err != nil {
		return err
	}
	if reconcileFirst && reconcile != nil {
		if err := reconcile(previous, next); err != nil {
			return fmt.Errorf("reconcile plugin Xray state: %w", err)
		}
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	resetFirewall := forceReset || next == nil || (effectiveTorrentEnabled(previous) && !effectiveTorrentEnabled(next))
	mutateFirewall := resetFirewall || previous == nil ||
		previous.firewallReady != snapshotFirewallReady(next) ||
		!firewallConfigsEqual(previousFirewall, nextFirewall)
	canMutateFirewall := s.firewallAvailableLocked()
	if resetFirewall && s.nft != nil {
		// A full Reset is the recovery primitive and may create tables from an
		// unowned or unhealthy backend state.
		canMutateFirewall = true
	}
	firewallApplied := false
	if mutateFirewall && canMutateFirewall {
		if err := s.applyFirewallLocked(ctx, nextFirewall, resetFirewall); err != nil {
			var rollbackErr error
			if !resetFirewall {
				rollbackErr = s.restoreFirewallLocked(previousFirewall)
			}
			if !s.firewallAvailableLocked() {
				degraded := previous
				if resetFirewall && next != nil {
					degraded = next
				}
				s.publishDegradedSnapshotLocked(degraded)
			}
			return errors.Join(
				fmt.Errorf("apply plugin firewall plan: %w", err),
				wrapFirewallRollbackError(rollbackErr),
			)
		}
		firewallApplied = true
	}
	if err := contextError(ctx); err != nil {
		if firewallApplied && resetFirewall {
			// Reset is irreversible because dynamic elements are gone. Publish
			// the matching snapshot even if the caller disconnected just after
			// the nft transaction completed.
			s.state.commitSnapshot(next)
			return nil
		}
		if firewallApplied && !resetFirewall {
			rollbackErr := s.restoreFirewallLocked(previousFirewall)
			if !s.firewallAvailableLocked() {
				s.publishDegradedSnapshotLocked(previous)
			}
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	if !reconcileFirst && reconcile != nil {
		if err := reconcile(previous, next); err != nil {
			if firewallApplied && !resetFirewall {
				rollbackErr := s.restoreFirewallLocked(previousFirewall)
				if rollbackErr != nil {
					if !s.firewallAvailableLocked() {
						s.publishDegradedSnapshotLocked(previous)
					}
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

func (s *Service) applyFirewallLocked(ctx context.Context, config firewallConfig, reset bool) error {
	if reset {
		return s.nft.Reset(ctx, config)
	}
	return s.nft.Apply(ctx, config)
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

func snapshotFirewallReady(snapshot *pluginSnapshot) bool {
	return snapshot != nil && snapshot.firewallReady
}

func (s *Service) publishDegradedSnapshotLocked(desired *pluginSnapshot) {
	if desired == nil {
		s.state.commitSnapshot(nil)
		return
	}
	next := *desired
	next.firewallReady = false
	s.state.commitSnapshot(&next)
}

func (s *Service) reconcileTorrentLocked(previous, next *pluginSnapshot) error {
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
	if err := validateBlockMutation(items); err != nil {
		slog.Warn("invalid nftables block request", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	if err := s.acquireMutation(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	defer s.releaseOperation()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.BlockIPs(ctx, items); err != nil {
		if !s.firewallAvailableLocked() {
			s.publishDegradedSnapshotLocked(s.state.currentSnapshot())
		}
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
	if err := validateUnblockMutation(ips); err != nil {
		slog.Warn("invalid nftables unblock request", "error", err)
		return AcceptedResponse{Accepted: false}
	}
	if err := s.acquireMutation(ctx); err != nil {
		return AcceptedResponse{Accepted: false}
	}
	defer s.releaseOperation()
	if s.readyLocked() != nil || !s.firewallAvailableLocked() {
		return AcceptedResponse{Accepted: false}
	}
	if err := s.nft.UnblockIPs(ctx, ips); err != nil {
		if !s.firewallAvailableLocked() {
			s.publishDegradedSnapshotLocked(s.state.currentSnapshot())
		}
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
	previous := s.state.currentSnapshot()
	next := previous
	if previous != nil && !previous.firewallReady {
		copy := *previous
		copy.firewallReady = true
		next = &copy
	}
	if err := s.applySnapshotResetReconcileFirstLocked(ctx, next, s.stopXrayLocked); err != nil {
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
	if s.nft == nil {
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
		visible := values[:min(len(values), maxLoggedDiagnosticValues)]
		slog.Warn("ASN prefixes not found", "asns", visible, "omitted", len(values)-len(visible))
	}
	if values := p.diagnostics.missingSharedListValues(); len(values) != 0 {
		visible := values[:min(len(values), maxLoggedDiagnosticValues)]
		slog.Warn("plugin shared lists not found", "lists", visible, "omitted", len(values)-len(visible))
	}
}

func pluginSnapshotsBehaviorEqual(left, right *pluginSnapshot) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.firewallReady == right.firewallReady &&
		left.whitelistIPs.equal(right.whitelistIPs) &&
		torrentSettingsEqual(left.torrent, right.torrent) &&
		firewallConfigsEqual(left.firewall, right.firewall)
}

func torrentSettingsEqual(left, right torrentSettings) bool {
	return left.enabled == right.enabled &&
		left.blockDuration == right.blockDuration &&
		hashIncludeRuleTags(left.includeRuleTags) == hashIncludeRuleTags(right.includeRuleTags) &&
		left.ignoredIPs.equal(right.ignoredIPs) &&
		maps.Equal(left.ignoredUsers, right.ignoredUsers)
}

func firewallConfigsEqual(left, right firewallConfig) bool {
	leftV4Ingress, leftV6Ingress := normalizeFilterPrefixes(left.ingressIPs)
	rightV4Ingress, rightV6Ingress := normalizeFilterPrefixes(right.ingressIPs)
	leftV4Egress, leftV6Egress := normalizeFilterPrefixes(left.egressIPs)
	rightV4Egress, rightV6Egress := normalizeFilterPrefixes(right.egressIPs)
	return slices.Equal(leftV4Ingress, rightV4Ingress) &&
		slices.Equal(leftV6Ingress, rightV6Ingress) &&
		slices.Equal(leftV4Egress, rightV4Egress) &&
		slices.Equal(leftV6Egress, rightV6Egress) &&
		slices.Equal(normalizedPorts(left.egressPorts), normalizedPorts(right.egressPorts))
}

func validateBlockMutation(items []BlockIP) error {
	if err := validateArrayLength("ips", len(items), maxNFTBlockBatch); err != nil {
		return err
	}
	for i, item := range items {
		if err := validateStringLength(fmt.Sprintf("ips[%d].ip", i), item.IP); err != nil {
			return err
		}
		if math.IsNaN(item.Timeout) || math.IsInf(item.Timeout, 0) || item.Timeout < 0 || item.Timeout > maxTorrentBlockDurationSec {
			return fmt.Errorf("ips[%d].timeout must be between 0 and %d seconds", i, maxTorrentBlockDurationSec)
		}
	}
	return nil
}

func validateUnblockMutation(ips []string) error {
	if err := validateArrayLength("ips", len(ips), maxNFTUnblockBatch); err != nil {
		return err
	}
	for i, ip := range ips {
		if err := validateStringLength(fmt.Sprintf("ips[%d]", i), ip); err != nil {
			return err
		}
	}
	return nil
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
