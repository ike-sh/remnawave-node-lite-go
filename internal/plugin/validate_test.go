package plugin

import "testing"

func validTorrentBlocker(enabled bool) map[string]any {
	return map[string]any{
		"enabled":       enabled,
		"blockDuration": 300,
		"ignoreLists":   map[string]any{},
	}
}

func TestValidatePluginConfigRejectsInvalidSharedLists(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"sharedLists": "bad",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidatePluginConfigAcceptsMinimalConfig(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"torrentBlocker": validTorrentBlocker(true),
		"sharedLists":    []any{},
	})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidatePluginConfigRequiresTorrentFields(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"torrentBlocker": map[string]any{"enabled": true},
	})
	if err == nil {
		t.Fatal("expected validation error for missing blockDuration/ignoreLists")
	}
}

func TestValidatePluginConfigRejectsInvalidCIDR(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"ingressFilter": map[string]any{
			"enabled":    true,
			"blockedIps": []any{"not-an-ip"},
		},
	})
	if err == nil {
		t.Fatal("expected validation error for invalid blocked IP")
	}
}

func TestValidatePluginConfigAcceptsExtReference(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"sharedLists": []any{
			map[string]any{
				"name":  "ext:blocked",
				"type":  "ipList",
				"items": []any{"10.0.0.0/8", "203.0.113.1"},
			},
		},
		"ingressFilter": map[string]any{
			"enabled":    true,
			"blockedIps": []any{"ext:blocked", "192.0.2.1"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidatePluginConfigRejectsEmptyIncludeRuleTags(t *testing.T) {
	t.Parallel()
	cfg := validTorrentBlocker(true)
	cfg["includeRuleTags"] = []any{}
	err := ValidatePluginConfig(map[string]any{"torrentBlocker": cfg})
	if err == nil {
		t.Fatal("expected validation error for empty includeRuleTags")
	}
}

func TestValidatePluginConfigRejectsInvalidPort(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"egressFilter": map[string]any{
			"enabled":      true,
			"blockedPorts": []any{70000},
		},
	})
	if err == nil {
		t.Fatal("expected validation error for invalid port")
	}
}

func TestValidatePluginConfigRejectsCIDRInWhitelist(t *testing.T) {
	t.Parallel()
	err := ValidatePluginConfig(map[string]any{
		"connectionDrop": map[string]any{
			"enabled":      true,
			"whitelistIps": []any{"10.0.0.0/8"},
		},
	})
	if err == nil {
		t.Fatal("connectionDrop whitelist must not accept CIDR")
	}
}

func TestBuildSharedIPMapUsesExtPrefix(t *testing.T) {
	t.Parallel()
	// buildSharedIPMap is unexported; exercise via resolve path in syncFilters indirectly
	cfg := map[string]any{
		"sharedLists": []any{
			map[string]any{
				"name":  "ext:mylist",
				"type":  "ipList",
				"items": []any{"203.0.113.10"},
			},
		},
	}
	m := buildSharedIPMap(cfg, nil)
	if _, ok := m["ext:mylist"]; !ok {
		t.Fatalf("expected ext:mylist key, got %#v", m)
	}
}
