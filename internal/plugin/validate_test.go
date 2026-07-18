package plugin

import (
	"context"
	"math"
	"strings"
	"testing"
)

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

func TestValidatePluginConfigAcceptsNonNegativeIntegerDurationAndExtEdges(t *testing.T) {
	t.Parallel()

	for _, duration := range []any{
		float64(0),
		float64(1),
		float64(31 * 24 * 60 * 60),
		float64(1 << 53),
		float64(1e20),
	} {
		cfg := validTorrentBlocker(true)
		cfg["blockDuration"] = duration
		cfg["ignoreLists"] = map[string]any{"ip": []any{"ext:"}}
		if err := ValidatePluginConfig(map[string]any{"torrentBlocker": cfg}); err != nil {
			t.Errorf("blockDuration=%v rejected: %v", duration, err)
		}
		request := mustSyncPlugin(t, map[string]any{
			"uuid":   "00000000-0000-4000-8000-000000000001",
			"name":   "duration-test",
			"config": map[string]any{"torrentBlocker": cfg},
		})
		if _, err := buildPluginPlan(request, nil, true); err != nil {
			t.Errorf("sync plan with blockDuration=%v rejected: %v", duration, err)
		}
	}
}

func TestValidatePluginConfigRejectsUnsafeBlockDuration(t *testing.T) {
	t.Parallel()

	for _, duration := range []any{float64(-1), math.Inf(1), math.NaN()} {
		cfg := validTorrentBlocker(true)
		cfg["blockDuration"] = duration
		if err := ValidatePluginConfig(map[string]any{"torrentBlocker": cfg}); err == nil {
			t.Errorf("blockDuration=%v was accepted", duration)
		}
	}
}

func TestValidatePluginConfigAcceptsDuplicateSharedListNames(t *testing.T) {
	t.Parallel()

	lists := []any{
		map[string]any{"name": "ext:duplicate", "type": "ipList", "items": []any{"192.0.2.1"}},
		map[string]any{"name": "ext:duplicate", "type": "ipList", "items": []any{"198.51.100.1"}},
	}
	if err := ValidatePluginConfig(map[string]any{"sharedLists": lists}); err != nil {
		t.Fatalf("duplicate shared list names should be accepted: %v", err)
	}
}

func TestValidatePluginConfigDuplicateSharedListsStillCountTowardItemBudget(t *testing.T) {
	t.Parallel()

	items := make([]any, maxSharedListItems)
	for i := range items {
		items[i] = "192.0.2.1"
	}
	lists := []any{
		map[string]any{"name": "ext:duplicate", "type": "ipList", "items": items},
		map[string]any{"name": "ext:duplicate", "type": "ipList", "items": items},
		map[string]any{"name": "ext:duplicate", "type": "ipList", "items": []any{"198.51.100.1"}},
	}
	if err := ValidatePluginConfig(map[string]any{"sharedLists": lists}); err == nil {
		t.Fatal("duplicate shared lists bypassed the total item budget")
	}
}

func TestValidatePluginConfigRejectsOversizedCollections(t *testing.T) {
	t.Parallel()

	lists := make([]any, maxSharedLists+1)
	for i := range lists {
		lists[i] = map[string]any{"name": "ext:x", "type": "ipList", "items": []any{}}
	}
	if err := ValidatePluginConfig(map[string]any{"sharedLists": lists}); err == nil {
		t.Fatal("oversized sharedLists was accepted")
	}

	items := make([]any, maxFilterItems+1)
	for i := range items {
		items[i] = "192.0.2.1"
	}
	if err := ValidatePluginConfig(map[string]any{
		"ingressFilter": map[string]any{"enabled": true, "blockedIps": items},
	}); err == nil {
		t.Fatal("oversized filter was accepted")
	}
}

func TestValidatePluginConfigTruncatesInvalidValueInError(t *testing.T) {
	t.Parallel()

	value := "bad" + strings.Repeat("x", maxPluginStringBytes*2)
	err := ValidatePluginConfig(map[string]any{
		"ingressFilter": map[string]any{"enabled": true, "blockedIps": []any{value}},
	})
	if err == nil {
		t.Fatal("invalid value was accepted")
	}
	if len(err.Error()) > maxLogValueBytes*2 {
		t.Fatalf("validation error retained oversized input: %d bytes", len(err.Error()))
	}
}

func TestQuotedErrorValueHasHardRenderedByteLimit(t *testing.T) {
	t.Parallel()

	got := quotedForError(strings.Repeat("\x00", maxPluginStringBytes))
	if len(got) > maxLogValueBytes {
		t.Fatalf("quoted error value = %d bytes, maximum is %d", len(got), maxLogValueBytes)
	}
}

func TestValidatePluginConfigRejectsExplicitNullSections(t *testing.T) {
	t.Parallel()

	for _, field := range []string{
		"sharedLists",
		"torrentBlocker",
		"ingressFilter",
		"egressFilter",
		"connectionDrop",
	} {
		if err := ValidatePluginConfig(map[string]any{field: nil}); err == nil {
			t.Errorf("explicit null %s was accepted", field)
		}
	}
}

func TestValidatePluginConfigRejectsFractionalBlockDuration(t *testing.T) {
	t.Parallel()

	cfg := validTorrentBlocker(true)
	cfg["blockDuration"] = 1.5
	if err := ValidatePluginConfig(map[string]any{"torrentBlocker": cfg}); err == nil {
		t.Fatal("fractional blockDuration was accepted")
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
	m, err := buildSharedIPMapWithDiagnosticsContext(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["ext:mylist"]; !ok {
		t.Fatalf("expected ext:mylist key, got %#v", m)
	}
}
