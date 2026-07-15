package contract_test

import (
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/httpserver"
)

// This list is independent evidence transcribed from the controllers at
// remnawave/node 2.8.0@596f015. The dispatcher consumes its own registry;
// comparing the two prevents a hand-maintained coverage map from self-passing.
var officialRoutes = []httpserver.NodeRoute{
	{Method: http.MethodPost, Path: "/node/xray/start"},
	{Method: http.MethodGet, Path: "/node/xray/stop"},
	{Method: http.MethodGet, Path: "/node/xray/healthcheck"},
	{Method: http.MethodPost, Path: "/node/stats/get-user-online-status"},
	{Method: http.MethodPost, Path: "/node/stats/get-users-stats"},
	{Method: http.MethodGet, Path: "/node/stats/get-system-stats"},
	{Method: http.MethodPost, Path: "/node/stats/get-inbound-stats"},
	{Method: http.MethodPost, Path: "/node/stats/get-outbound-stats"},
	{Method: http.MethodPost, Path: "/node/stats/get-all-outbounds-stats"},
	{Method: http.MethodPost, Path: "/node/stats/get-all-inbounds-stats"},
	{Method: http.MethodPost, Path: "/node/stats/get-combined-stats"},
	{Method: http.MethodPost, Path: "/node/stats/get-user-ip-list"},
	{Method: http.MethodGet, Path: "/node/stats/get-users-ip-list"},
	{Method: http.MethodPost, Path: "/node/handler/add-user"},
	{Method: http.MethodPost, Path: "/node/handler/remove-user"},
	{Method: http.MethodPost, Path: "/node/handler/get-inbound-users-count"},
	{Method: http.MethodPost, Path: "/node/handler/get-inbound-users"},
	{Method: http.MethodPost, Path: "/node/handler/add-users"},
	{Method: http.MethodPost, Path: "/node/handler/remove-users"},
	{Method: http.MethodPost, Path: "/node/handler/drop-users-connections"},
	{Method: http.MethodPost, Path: "/node/handler/drop-ips"},
	{Method: http.MethodPost, Path: "/node/plugin/sync"},
	{Method: http.MethodPost, Path: "/node/plugin/torrent-blocker/collect"},
	{Method: http.MethodPost, Path: "/node/plugin/nftables/block-ips"},
	{Method: http.MethodPost, Path: "/node/plugin/nftables/unblock-ips"},
	{Method: http.MethodPost, Path: "/node/plugin/nftables/recreate-tables"},
}

func TestOfficialRouteRegistry(t *testing.T) {
	t.Parallel()

	want := append([]httpserver.NodeRoute(nil), officialRoutes...)
	sortRoutes(want)
	got := httpserver.RegisteredNodeRoutes()

	if len(got) != 26 {
		t.Fatalf("registered route count = %d, want 26", len(got))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("registered routes do not match official 2.8.0\n got: %#v\nwant: %#v", got, want)
	}
}

func sortRoutes(routes []httpserver.NodeRoute) {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
}
