package contract_test

import (
	"os"
	"strings"
	"testing"
)

// Official @remnawave/node REST paths (from libs/contract/api/routes.ts).
// Baseline: upstream v3.4.1 (4491263) — contract-sync CI tracks this exact tag.
var officialRoutes = []string{
	"/node/xray/start",
	"/node/xray/stop",
	"/node/xray/healthcheck",
	"/node/stats/get-user-online-status",
	"/node/stats/get-geocheck",
	"/node/stats/get-users-stats",
	"/node/stats/get-system-stats",
	"/node/stats/get-inbound-stats",
	"/node/stats/get-outbound-stats",
	"/node/stats/get-all-outbounds-stats",
	"/node/stats/get-all-inbounds-stats",
	"/node/stats/get-combined-stats",
	"/node/stats/get-user-ip-list",
	"/node/stats/get-users-ip-list",
	"/node/handler/add-user",
	"/node/handler/remove-user",
	"/node/handler/add-users",
	"/node/handler/remove-users",
	"/node/handler/drop-users-connections",
	"/node/handler/drop-ips",
	"/node/plugin/sync",
	"/node/plugin/torrent-blocker/collect",
	"/node/plugin/nftables/block-ips",
	"/node/plugin/nftables/unblock-ips",
	"/node/plugin/nftables/recreate-tables",
}

// liteGoImplemented marks routes wired in this repository.
var liteGoImplemented = map[string]bool{
	"/node/xray/start":                      true,
	"/node/xray/stop":                       true,
	"/node/xray/healthcheck":                true,
	"/node/stats/get-user-online-status":    true,
	"/node/stats/get-geocheck":              true,
	"/node/stats/get-users-stats":           true,
	"/node/stats/get-system-stats":          true,
	"/node/stats/get-inbound-stats":         true,
	"/node/stats/get-outbound-stats":        true,
	"/node/stats/get-all-outbounds-stats":   true,
	"/node/stats/get-all-inbounds-stats":    true,
	"/node/stats/get-combined-stats":        true,
	"/node/stats/get-user-ip-list":          true,
	"/node/stats/get-users-ip-list":         true,
	"/node/handler/add-user":                true,
	"/node/handler/remove-user":             true,
	"/node/handler/add-users":               true,
	"/node/handler/remove-users":            true,
	"/node/handler/drop-users-connections":  true,
	"/node/handler/drop-ips":                true,
	"/node/plugin/sync":                     true,
	"/node/plugin/torrent-blocker/collect":  true,
	"/node/plugin/nftables/block-ips":       true,
	"/node/plugin/nftables/unblock-ips":     true,
	"/node/plugin/nftables/recreate-tables": true,
}

func TestOfficialRoutesCoverage(t *testing.T) {
	t.Parallel()
	if got := len(officialRoutes); got != 25 {
		t.Fatalf("official route count = %d, want 25", got)
	}
	if got := len(liteGoImplemented); got != 25 {
		t.Fatalf("implemented route count = %d, want 25", got)
	}

	for _, route := range officialRoutes {
		if !liteGoImplemented[route] {
			t.Fatalf("route %s not marked implemented in lite-go", route)
		}
	}
	for route := range liteGoImplemented {
		found := false
		for _, official := range officialRoutes {
			if official == route {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("extra route %s", route)
		}
	}
}

func TestOfficialMethodPathSnapshot(t *testing.T) {
	raw, err := os.ReadFile("routes.snapshot")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 25 {
		t.Fatalf("snapshot count = %d, want 25", len(lines))
	}
	seen := make(map[string]bool)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != "GET" && fields[0] != "POST") {
			t.Fatalf("bad snapshot line %q", line)
		}
		if seen[fields[1]] || !liteGoImplemented[fields[1]] {
			t.Fatalf("duplicate or unknown route %q", fields[1])
		}
		seen[fields[1]] = true
	}
}
