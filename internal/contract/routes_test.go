package contract_test

import (
	"testing"
)
// Official @remnawave/node REST paths (from libs/contract/api/routes.ts).
// Baseline: upstream v2.7.0 (2026-03-28) — contract-sync CI tracks main weekly.
var officialRoutes = []string{
	"/node/xray/start",
	"/node/xray/stop",
	"/node/xray/healthcheck",
	"/node/stats/get-user-online-status",
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
	"/node/handler/get-inbound-users-count",
	"/node/handler/get-inbound-users",
	"/node/handler/add-users",
	"/node/handler/remove-users",
	"/node/handler/drop-users-connections",
	"/node/handler/drop-ips",
	"/node/plugin/sync",
	"/node/plugin/torrent-blocker/collect",
	"/node/plugin/nftables/block-ips",
	"/node/plugin/nftables/unblock-ips",
	"/node/plugin/nftables/recreate-tables",
	"/vision/block-ip",
	"/vision/unblock-ip",
}

// liteGoImplemented marks routes wired in this repository.
var liteGoImplemented = map[string]bool{
	"/node/xray/start":                      true,
	"/node/xray/stop":                       true,
	"/node/xray/healthcheck":                true,
	"/node/stats/get-user-online-status":      true,
	"/node/stats/get-users-stats":             true,
	"/node/stats/get-system-stats":            true,
	"/node/stats/get-inbound-stats":           true,
	"/node/stats/get-outbound-stats":          true,
	"/node/stats/get-all-outbounds-stats":    true,
	"/node/stats/get-all-inbounds-stats":      true,
	"/node/stats/get-combined-stats":          true,
	"/node/stats/get-user-ip-list":            true,
	"/node/stats/get-users-ip-list":           true,
	"/node/handler/add-user":                  true,
	"/node/handler/remove-user":               true,
	"/node/handler/get-inbound-users-count":   true,
	"/node/handler/get-inbound-users":         true,
	"/node/handler/add-users":                 true,
	"/node/handler/remove-users":              true,
	"/node/handler/drop-users-connections":    true,
	"/node/handler/drop-ips":                  true,
	"/node/plugin/sync":                       true,
	"/node/plugin/torrent-blocker/collect":    true,
	"/node/plugin/nftables/block-ips":         true,
	"/node/plugin/nftables/unblock-ips":       true,
	"/node/plugin/nftables/recreate-tables":   true,
	"/vision/block-ip":                        true,
	"/vision/unblock-ip":                      true,
}

func TestOfficialRoutesCoverage(t *testing.T) {
	t.Parallel()

	for _, route := range officialRoutes {
		if !liteGoImplemented[route] {
			t.Fatalf("route %s not marked implemented in lite-go", route)
		}
	}
}
