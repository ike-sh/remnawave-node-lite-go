package httpserver

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/bodylimit"
)

func TestEveryRegisteredRouteHasExpectedRequestBodyBudget(t *testing.T) {
	expected := map[nodeRouteID]nodeRequestBodyClass{
		routeXrayStart:                   nodeRequestBodyBulk,
		routeXrayStop:                    nodeRequestBodySmall,
		routeXrayHealthcheck:             nodeRequestBodySmall,
		routeStatsGetUserOnlineStatus:    nodeRequestBodySmall,
		routeStatsGetUsersStats:          nodeRequestBodySmall,
		routeStatsGetSystemStats:         nodeRequestBodySmall,
		routeStatsGetInboundStats:        nodeRequestBodySmall,
		routeStatsGetOutboundStats:       nodeRequestBodySmall,
		routeStatsGetAllOutboundsStats:   nodeRequestBodySmall,
		routeStatsGetAllInboundsStats:    nodeRequestBodySmall,
		routeStatsGetCombinedStats:       nodeRequestBodySmall,
		routeStatsGetUserIPList:          nodeRequestBodySmall,
		routeStatsGetUsersIPList:         nodeRequestBodySmall,
		routeHandlerAddUser:              nodeRequestBodyMedium,
		routeHandlerRemoveUser:           nodeRequestBodySmall,
		routeHandlerGetInboundUsersCount: nodeRequestBodySmall,
		routeHandlerGetInboundUsers:      nodeRequestBodySmall,
		routeHandlerAddUsers:             nodeRequestBodyBulk,
		routeHandlerRemoveUsers:          nodeRequestBodyBulk,
		routeHandlerDropUsersConnections: nodeRequestBodyBulk,
		routeHandlerDropIPs:              nodeRequestBodyBulk,
		routePluginSync:                  nodeRequestBodyBulk,
		routePluginCollectTorrentReports: nodeRequestBodySmall,
		routePluginBlockIPs:              nodeRequestBodyMedium,
		routePluginUnblockIPs:            nodeRequestBodyMedium,
		routePluginRecreateTables:        nodeRequestBodySmall,
	}
	if len(expected) != len(nodeRouteDefinitions) {
		t.Fatalf("budget matrix has %d routes, registry has %d", len(expected), len(nodeRouteDefinitions))
	}

	seen := make(map[nodeRouteID]struct{}, len(nodeRouteDefinitions))
	for _, definition := range nodeRouteDefinitions {
		want, ok := expected[definition.id]
		if !ok {
			t.Errorf("%s %s has no expected request budget", definition.Method, definition.Path)
			continue
		}
		seen[definition.id] = struct{}{}
		if got := nodeRouteRequestBodyClass(definition.id); got != want {
			t.Errorf("%s %s class = %d, want %d", definition.Method, definition.Path, got, want)
		}
		wantLimit := map[nodeRequestBodyClass]int64{
			nodeRequestBodySmall:  smallRequestBodyBytes,
			nodeRequestBodyMedium: mediumRequestBodyBytes,
			nodeRequestBodyBulk:   bulkRequestBodyBytes,
		}[want]
		if got := nodeRouteRequestBodyLimit(definition.id); got != wantLimit {
			t.Errorf("%s %s limit = %d, want %d", definition.Method, definition.Path, got, wantLimit)
		}
	}
	if len(seen) != len(expected) {
		t.Fatalf("observed %d budgeted routes, want %d", len(seen), len(expected))
	}
	if got := nodeRouteRequestBodyLimit(0); got != smallRequestBodyBytes {
		t.Fatalf("unknown route limit = %d, want conservative %d", got, smallRequestBodyBytes)
	}
}

func TestNodeRequestBodyBudgetHonorsConfiguredCeiling(t *testing.T) {
	if err := bodylimit.Configure(false, 1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bodylimit.Configure(false, 0) })

	for _, definition := range nodeRouteDefinitions {
		var got int64
		handler := withNodeRequestBodyLimit(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = bodylimit.RequestLimit(r)
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(definition.Method, definition.Path, nil))

		want := nodeRouteRequestBodyLimit(definition.id)
		if want > 1<<20 {
			want = 1 << 20
		}
		if got != want {
			t.Errorf("%s %s effective limit = %d, want %d", definition.Method, definition.Path, got, want)
		}
	}
}

func TestUnknownRouteNeverReceivesElevatedRequestBudget(t *testing.T) {
	if err := bodylimit.Configure(false, 0); err != nil {
		t.Fatal(err)
	}

	var got int64
	withNodeRequestBodyLimit(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = bodylimit.RequestLimit(r)
	})).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/node/unknown", nil),
	)
	if got != smallRequestBodyBytes {
		t.Fatalf("unknown route effective limit = %d, want %d", got, smallRequestBodyBytes)
	}
}

func TestRouteRequestBodyLimitsAreEnforcedByClass(t *testing.T) {
	if err := bodylimit.Configure(false, 0); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name      string
		path      string
		bodyBytes int
		wantLarge bool
	}{
		{name: "small rejects above 64 KiB", path: "/node/stats/get-users-stats", bodyBytes: 65 << 10, wantLarge: true},
		{name: "medium accepts above small budget", path: "/node/handler/add-user", bodyBytes: 65 << 10},
		{name: "medium rejects above 256 KiB", path: "/node/plugin/nftables/block-ips", bodyBytes: 257 << 10, wantLarge: true},
		{name: "bulk accepts above medium budget", path: "/node/plugin/sync", bodyBytes: 257 << 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			var readErr error
			handler := withNodeRequestBodyLimit(bodylimit.LimitMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				_, readErr = io.Copy(io.Discard, r.Body)
			})))
			handler.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(make([]byte, test.bodyBytes))),
			)

			var limitError *http.MaxBytesError
			if got := errors.As(readErr, &limitError); got != test.wantLarge {
				t.Fatalf("read error = %v, payload-too-large=%v; want %v", readErr, got, test.wantLarge)
			}
		})
	}
}
