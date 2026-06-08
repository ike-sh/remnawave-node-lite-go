package xray

import "testing"

func TestGenerateAPIConfigTorrentBlocker(t *testing.T) {
	t.Parallel()

	cfg := generateAPIConfig(map[string]any{
		"inbounds":  []any{map[string]any{"tag": "in-1"}},
		"outbounds": []any{},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"ruleTag": "custom", "domain": []any{"example.com"}},
			},
		},
	}, 61000, internalCerts{}, TorrentBlockerOptions{
		Enabled:         true,
		IncludeRuleTags: []string{"custom"},
		SocketPath:      "/run/test.sock",
		RESTToken:       "token",
	})

	outbounds := arrayFrom(cfg["outbounds"])
	foundOutbound := false
	for _, item := range outbounds {
		outbound, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if outbound["tag"] == torrentBlockerOutboundTag {
			foundOutbound = true
		}
	}
	if !foundOutbound {
		t.Fatal("expected torrent blocker outbound")
	}

	routing, ok := cfg["routing"].(map[string]any)
	if !ok {
		t.Fatal("missing routing")
	}
	rules := arrayFrom(routing["rules"])
	if len(rules) < 2 {
		t.Fatalf("expected at least 2 rules, got %d", len(rules))
	}
}
