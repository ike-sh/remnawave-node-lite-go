package plugin

import (
	"context"
	"log/slog"
	"math"
	"net"
	"regexp"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xraywebhook"
)

var sourceIPPattern = regexp.MustCompile(`^(?:(?:tcp|udp):)?(?:\[(.+?)\]|(.+?))(?::(\d+))?$`)

var defaultIgnoredIPs = map[string]struct{}{
	"::":              {},
	"::1":             {},
	"0.0.0.0":         {},
	"0.0.0.0/0":       {},
	"127.0.0.0/8":     {},
	"127.0.0.1":       {},
	"255.255.255.255": {},
}

type torrentSettings struct {
	enabled         bool
	blockDuration   float64
	includeRuleTags []string
	ignoredIPs      map[string]struct{}
	ignoredUsers    map[string]struct{}
}

func (s *State) TorrentBlockerEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active != nil && s.active.torrent.enabled
}

func (s *State) TorrentBlockerIncludeRuleTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil || len(s.active.torrent.includeRuleTags) == 0 {
		return nil
	}
	return append([]string(nil), s.active.torrent.includeRuleTags...)
}

func (s *Service) HandleXrayWebhook(payload xraywebhook.Payload) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if s.readyLocked() != nil {
		return
	}

	snapshot := s.state.currentSnapshot()
	if snapshot == nil || !snapshot.torrent.enabled {
		return
	}

	if payload.Email == nil || payload.Source == nil {
		return
	}
	email := *payload.Email
	ip := extractWebhookIP(*payload.Source)
	if ip == "" || email == "" {
		return
	}
	if torrentIPIgnored(snapshot.torrent, ip) || torrentUserIgnored(snapshot.torrent, email) {
		return
	}

	duration := snapshot.torrent.blockDuration
	blocked := false
	if s.firewallAvailableLocked() {
		if err := s.nft.BlockIPs([]BlockIP{{IP: ip, Timeout: duration}}); err != nil {
			slog.Warn("torrent blocker failed to block ip", "ip", ip, "error", err)
		} else {
			blocked = true
			if s.dropper != nil {
				s.dropper.DropIPs(context.Background(), []string{ip})
			}
		}
	}

	now := time.Now().UTC()
	s.state.AddReport(TorrentReport{
		ActionReport: struct {
			Blocked       bool      `json:"blocked"`
			IP            string    `json:"ip"`
			BlockDuration float64   `json:"blockDuration"`
			WillUnblockAt time.Time `json:"willUnblockAt"`
			UserID        string    `json:"userId"`
			ProcessedAt   time.Time `json:"processedAt"`
		}{
			Blocked:       blocked,
			IP:            ip,
			BlockDuration: duration,
			WillUnblockAt: addSeconds(now, duration),
			UserID:        email,
			ProcessedAt:   now,
		},
		XrayReport: payload,
	})
}

func torrentIPIgnored(settings torrentSettings, ip string) bool {
	if _, ok := defaultIgnoredIPs[ip]; ok {
		return true
	}
	_, ok := settings.ignoredIPs[ip]
	return ok
}

func torrentUserIgnored(settings torrentSettings, userID string) bool {
	_, ok := settings.ignoredUsers[userID]
	return ok
}

func addSeconds(at time.Time, seconds float64) time.Time {
	maxSeconds := float64(math.MaxInt64) / float64(time.Second)
	minSeconds := float64(math.MinInt64) / float64(time.Second)
	switch {
	case seconds >= maxSeconds:
		return at.Add(time.Duration(math.MaxInt64))
	case seconds <= minSeconds:
		return at.Add(time.Duration(math.MinInt64))
	default:
		return at.Add(time.Duration(seconds * float64(time.Second)))
	}
}

func extractWebhookIP(source string) string {
	if source == "" {
		return ""
	}
	match := sourceIPPattern.FindStringSubmatch(source)
	candidate := source
	if len(match) > 0 {
		if match[1] != "" {
			candidate = match[1]
		} else if match[2] != "" {
			candidate = match[2]
		}
	}
	if net.ParseIP(candidate) == nil {
		return ""
	}
	return candidate
}
