package plugin

import (
	"log/slog"
	"net"
	"regexp"
	"strings"
	"time"
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
	blockDuration   int
	includeRuleTags []string
	ignoredIPs      map[string]struct{}
	ignoredUsers    map[string]struct{}
}

func (s *State) configureTorrentBlocker(rawConfig map[string]any, shared map[string][]string) {
	s.torrent = torrentSettings{
		ignoredIPs:   make(map[string]struct{}),
		ignoredUsers: make(map[string]struct{}),
	}
	blocker, ok := rawConfig["torrentBlocker"].(map[string]any)
	if !ok {
		return
	}
	enabled, _ := blocker["enabled"].(bool)
	if !enabled {
		return
	}
	duration := 300
	if value, ok := blocker["blockDuration"].(float64); ok && value > 0 {
		duration = int(value)
	}
	s.torrent.enabled = true
	s.torrent.blockDuration = duration
	s.torrent.includeRuleTags = toStringSlice(blocker["includeRuleTags"])

	if ignoreLists, ok := blocker["ignoreLists"].(map[string]any); ok {
		for _, ip := range resolveIPList(toStringSlice(ignoreLists["ip"]), shared) {
			s.torrent.ignoredIPs[ip] = struct{}{}
		}
		for _, user := range toIntStringSlice(ignoreLists["userId"]) {
			s.torrent.ignoredUsers[user] = struct{}{}
		}
	}
}

func (s *State) TorrentBlockerEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.torrent.enabled
}

func (s *State) TorrentBlockerIncludeRuleTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.torrent.includeRuleTags) == 0 {
		return nil
	}
	out := append([]string(nil), s.torrent.includeRuleTags...)
	return out
}

func (s *State) isTorrentIPIgnored(ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := defaultIgnoredIPs[ip]; ok {
		return true
	}
	_, ok := s.torrent.ignoredIPs[ip]
	return ok
}

func (s *State) isTorrentUserIgnored(userID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.torrent.ignoredUsers[userID]
	return ok
}

func (s *State) torrentEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.torrent.enabled
}

func (s *State) torrentBlockDuration() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.torrent.blockDuration <= 0 {
		return 300
	}
	return s.torrent.blockDuration
}

func (s *Service) HandleXrayWebhook(payload map[string]any) {
	if !s.state.torrentEnabled() {
		return
	}

	email, _ := payload["email"].(string)
	source, _ := payload["source"].(string)
	ip := extractWebhookIP(source)
	if ip == "" || email == "" {
		return
	}
	if s.state.isTorrentIPIgnored(ip) || s.state.isTorrentUserIgnored(email) {
		return
	}

	duration := s.state.torrentBlockDuration()
	blocked := false
	if s.nft.Available() {
		if err := s.nft.blockIP(ip, duration); err != nil {
			slog.Warn("torrent blocker failed to block ip", "ip", ip, "error", err)
		} else {
			blocked = true
			if s.dropper != nil {
				s.dropper.DropIPs([]string{ip})
			}
		}
	}

	now := time.Now().UTC()
	s.state.AddReport(TorrentReport{
		ActionReport: struct {
			Blocked       bool      `json:"blocked"`
			IP            string    `json:"ip"`
			BlockDuration int       `json:"blockDuration"`
			WillUnblockAt time.Time `json:"willUnblockAt"`
			UserID        string    `json:"userId"`
			ProcessedAt   time.Time `json:"processedAt"`
		}{
			Blocked:       blocked,
			IP:            ip,
			BlockDuration: duration,
			WillUnblockAt: now.Add(time.Duration(duration) * time.Second),
			UserID:        email,
			ProcessedAt:   now,
		},
		XrayReport: payload,
	})
}

func ExtractWebhookIPForTest(source string) string {
	return extractWebhookIP(source)
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

func resolveIPList(items []string, shared map[string][]string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item, "ext:") {
			if resolved, ok := shared[item]; ok {
				out = append(out, resolved...)
			}
			continue
		}
		out = append(out, item)
	}
	return out
}

func buildSharedIPMap(rawConfig map[string]any) map[string][]string {
	shared := make(map[string][]string)
	lists, ok := rawConfig["sharedLists"].([]any)
	if !ok {
		return shared
	}
	for _, item := range lists {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if entryType, _ := entry["type"].(string); entryType != "ipList" {
			continue
		}
		name, _ := entry["name"].(string)
		if name == "" {
			continue
		}
		key := name
		if !strings.HasPrefix(name, "ext:") {
			key = "ext:" + name
		}
		shared[key] = toStringSlice(entry["items"])
	}
	return shared
}

func (s *Service) syncFilters(cfg map[string]any) {
	shared := buildSharedIPMap(cfg)
	if ingress, ok := cfg["ingressFilter"].(map[string]any); ok {
		if enabled, _ := ingress["enabled"].(bool); enabled {
			ips := resolveIPList(toStringSlice(ingress["blockedIps"]), shared)
			_ = s.nft.syncIngressFilter(ips)
		}
	}
	if egress, ok := cfg["egressFilter"].(map[string]any); ok {
		if enabled, _ := egress["enabled"].(bool); enabled {
			ips := resolveIPList(toStringSlice(egress["blockedIps"]), shared)
			ports := toIntSlice(egress["blockedPorts"])
			_ = s.nft.syncEgressFilter(ips, ports)
		}
	}
}

func toIntSlice(value any) []int {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case float64:
			out = append(out, int(v))
		case int:
			out = append(out, v)
		}
	}
	return out
}
