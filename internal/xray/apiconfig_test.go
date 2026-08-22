package xray

import "testing"

func TestGenerateAPIConfigDedupesAPIRoutingRule(t *testing.T) {
	t.Parallel()

	config := generateAPIConfig(map[string]any{
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"inboundTag": []any{apiInboundTag}, "outboundTag": apiTag},
				map[string]any{"outboundTag": "direct"},
			},
		},
	}, "remnanode-xtls-test", TorrentBlockerOptions{})

	routing := config["routing"].(map[string]any)
	apiRules := 0
	for _, item := range arrayFrom(routing["rules"]) {
		rule, _ := item.(map[string]any)
		if tag, _ := rule["outboundTag"].(string); tag == apiTag {
			apiRules++
		}
	}
	if apiRules != 1 {
		t.Fatalf("expected exactly 1 %s routing rule after dedupe, got %d", apiTag, apiRules)
	}
}

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
	}, "remnanode-xtls-test", TorrentBlockerOptions{
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

func TestGenerateAPIConfigTorrentRulePlacement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		placement float64
		want      []string
	}{
		{name: "default", placement: 0, want: []string{"api", "torrent", "a", "b"}},
		{name: "after first user rule", placement: 1, want: []string{"api", "a", "torrent", "b"}},
		{name: "after all user rules", placement: 2, want: []string{"api", "a", "b", "torrent"}},
		{name: "clamped", placement: 1000, want: []string{"api", "a", "b", "torrent"}},
		{name: "fraction falls back", placement: 1.5, want: []string{"api", "torrent", "a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := generateAPIConfig(map[string]any{
				"routing": map[string]any{"rules": []any{
					map[string]any{"ruleTag": "a", "outboundTag": "direct"},
					map[string]any{"ruleTag": "b", "outboundTag": "direct"},
				}},
			}, "remnanode-xtls-test", TorrentBlockerOptions{Enabled: true, RulePlacement: tc.placement})
			routing := config["routing"].(map[string]any)
			got := make([]string, 0, 4)
			for _, item := range arrayFrom(routing["rules"]) {
				rule := item.(map[string]any)
				switch {
				case rule["outboundTag"] == apiTag:
					got = append(got, "api")
				case rule["outboundTag"] == torrentBlockerOutboundTag:
					got = append(got, "torrent")
				default:
					got = append(got, rule["ruleTag"].(string))
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("rule order = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("rule order = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
