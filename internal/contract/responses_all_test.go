package contract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodehandler"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/stats"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xray"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

var responseShapeTests = map[string]func(t *testing.T) []byte{
	"/node/xray/start":                      testXrayStartResponseShape,
	"/node/xray/stop":                       testXrayStopResponseShape,
	"/node/xray/healthcheck":                testXrayHealthcheckResponseShape,
	"/node/stats/get-user-online-status":    testGetUserOnlineStatusResponseShape,
	"/node/stats/get-system-stats":          testGetSystemStatsResponseShape,
	"/node/stats/get-users-stats":           testGetUsersStatsResponseShape,
	"/node/stats/get-inbound-stats":         testGetInboundStatsResponseShape,
	"/node/stats/get-outbound-stats":        testGetOutboundStatsResponseShape,
	"/node/stats/get-all-inbounds-stats":    testGetAllInboundsStatsResponseShape,
	"/node/stats/get-all-outbounds-stats":   testGetAllOutboundsStatsResponseShape,
	"/node/stats/get-combined-stats":        testGetCombinedStatsResponseShape,
	"/node/stats/get-user-ip-list":          testGetUserIPListResponseShape,
	"/node/stats/get-users-ip-list":         testGetUsersIPListResponseShape,
	"/node/handler/add-user":                testAddUserResponseShape,
	"/node/handler/remove-user":             testRemoveUserResponseShape,
	"/node/handler/get-inbound-users-count": testGetInboundUsersCountResponseShape,
	"/node/handler/get-inbound-users":       testGetInboundUsersResponseShape,
	"/node/handler/add-users":               testAddUsersResponseShape,
	"/node/handler/remove-users":            testRemoveUsersResponseShape,
	"/node/handler/drop-users-connections":  testDropUsersConnectionsResponseShape,
	"/node/handler/drop-ips":                testDropIPsResponseShape,
	"/node/plugin/sync":                     testPluginSyncResponseShape,
	"/node/plugin/torrent-blocker/collect":  testPluginCollectReportsResponseShape,
	"/node/plugin/nftables/block-ips":       testPluginBlockIPsResponseShape,
	"/node/plugin/nftables/unblock-ips":     testPluginUnblockIPsResponseShape,
	"/node/plugin/nftables/recreate-tables": testPluginRecreateTablesResponseShape,
}

func TestOfficialResponseShapes(t *testing.T) {
	for _, route := range officialRoutes {
		route := route
		t.Run(route.Path, func(t *testing.T) {
			t.Parallel()
			fn, ok := responseShapeTests[route.Path]
			if !ok {
				t.Fatalf("missing response shape test for %s", route.Path)
			}
			raw := fn(t)
			if err := contractspec.ValidateResponse(route.Path, raw); err != nil {
				t.Fatalf("response violates official schema: %v\n%s", err, raw)
			}
		})
	}
}

func officialRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	route, ok := contractspec.FindRouteByPath(path)
	if !ok {
		t.Fatalf("route %s is missing from contract evidence", path)
	}
	if len(route.ValidRequest) == 0 {
		return httptest.NewRequest(route.Method, route.Path, nil)
	}
	return httptest.NewRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest))
}

func testManager(t *testing.T) *xray.Manager {
	t.Helper()
	manager, err := xray.NewManager(xray.Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             t.TempDir(),
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token",
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

func encodeEnvelope(response any) []byte {
	body, _ := json.Marshal(map[string]any{"response": response})
	return body
}

func testXrayStartResponseShape(t *testing.T) []byte {
	manager := testManager(t)
	resp := manager.Start(context.Background(), xray.StartRequest{
		XrayConfig: map[string]any{"inbounds": []any{}},
	})
	raw := encodeEnvelope(resp)
	return raw
}

func testXrayStopResponseShape(t *testing.T) []byte {
	manager := testManager(t)
	raw := encodeEnvelope(manager.Stop(true))
	return raw
}

func testXrayHealthcheckResponseShape(t *testing.T) []byte {
	manager := testManager(t)
	raw := encodeEnvelope(manager.Health())
	return raw
}

func statsService(t *testing.T) *stats.Service {
	t.Helper()
	return stats.NewService(stubStatsProvider{}, stubReportsCounter{})
}

func testGetUserOnlineStatusResponseShape(t *testing.T) []byte {
	service := statsService(t)
	return encodeEnvelope(service.GetUserOnlineStatus(context.Background(), "user-1"))
}

func testGetSystemStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetSystemStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetUsersStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetUsersStats(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetInboundStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetInboundStats(context.Background(), "inbound-1", false)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetOutboundStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetOutboundStats(context.Background(), "outbound-1", false)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetAllInboundsStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetAllInboundsStats(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetAllOutboundsStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetAllOutboundsStats(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetCombinedStatsResponseShape(t *testing.T) []byte {
	service := statsService(t)
	response, err := service.GetCombinedStats(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetUserIPListResponseShape(t *testing.T) []byte {
	service := statsService(t)
	return encodeEnvelope(service.GetUserIPList(context.Background(), "user-1"))
}

func testGetUsersIPListResponseShape(t *testing.T) []byte {
	service := statsService(t)
	return encodeEnvelope(service.GetUsersIPList(context.Background()))
}

func handlerService() *nodehandler.Service {
	return nodehandler.NewService(stubHandlerProvider{}, connections.NewDropper(nil))
}

func testRemoveUserResponseShape(t *testing.T) []byte {
	service := handlerService()
	response, err := service.RemoveUser(context.Background(), nodehandler.RemoveUserRequest{
		Username:  "user-1",
		VlessUUID: "00000000-0000-4000-8000-000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testGetInboundUsersResponseShape(t *testing.T) []byte {
	service := handlerService()
	response, err := service.GetInboundUsers(context.Background(), "inbound-1")
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testAddUsersResponseShape(t *testing.T) []byte {
	service := handlerService()
	response, err := service.AddUsers(context.Background(), nodehandler.AddUsersRequest{
		AffectedInboundTags: []string{"inbound-1"},
		Users: []nodehandler.BatchUser{{
			InboundData: []nodehandler.BatchInbound{{Type: "vless", Tag: "inbound-1", Flow: ""}},
			UserData: nodehandler.BatchUserData{
				UserID:         "user-1",
				HashUUID:       "00000000-0000-4000-8000-000000000001",
				VlessUUID:      "00000000-0000-4000-8000-000000000002",
				TrojanPassword: "trojan-secret",
				SSPassword:     "ss-secret",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testRemoveUsersResponseShape(t *testing.T) []byte {
	service := handlerService()
	response, err := service.RemoveUsers(context.Background(), nodehandler.RemoveUsersRequest{
		Users: []nodehandler.RemoveUsersItem{{
			UserID:   "user-1",
			HashUUID: "00000000-0000-4000-8000-000000000001",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testDropIPsResponseShape(t *testing.T) []byte {
	service := handlerService()
	return encodeEnvelope(service.DropIPs([]string{"203.0.113.10"}))
}

func testAddUserResponseShape(t *testing.T) []byte {
	service := handlerService()
	response, err := service.AddUser(context.Background(), nodehandler.AddUserRequest{
		Data: []nodehandler.AddUserItem{{
			Type: "vless", Tag: "inbound-1", Username: "user-1",
			UUID: "00000000-0000-4000-8000-000000000001", Flow: "",
		}},
		HashData: nodehandler.AddUserHashData{VlessUUID: "00000000-0000-4000-8000-000000000002"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testDropUsersConnectionsResponseShape(t *testing.T) []byte {
	service := handlerService()
	return encodeEnvelope(service.DropUsersConnections(context.Background(), []string{"user-1"}))
}

func testGetInboundUsersCountResponseShape(t *testing.T) []byte {
	service := handlerService()
	response, err := service.GetInboundUsersCount(context.Background(), "inbound-1")
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(response)
}

func testPluginSyncResponseShape(t *testing.T) []byte {
	service := pluginService()
	request, err := plugin.NewSyncPlugin(
		"00000000-0000-4000-8000-000000000001",
		"node-plugin",
		map[string]any{},
	)
	if err != nil {
		t.Fatal(err)
	}
	return encodeEnvelope(service.Sync(request))
}

func testPluginCollectReportsResponseShape(t *testing.T) []byte {
	state := plugin.NewState()
	var report plugin.TorrentReport
	report.ActionReport.Blocked = true
	report.ActionReport.IP = "203.0.113.10"
	report.ActionReport.BlockDuration = 60
	report.ActionReport.WillUnblockAt = time.Now().Add(time.Minute)
	report.ActionReport.UserID = "user-1"
	report.ActionReport.ProcessedAt = time.Now()
	report.XrayReport = map[string]any{
		"email":          "user-1",
		"level":          1,
		"protocol":       "vless",
		"network":        "tcp",
		"source":         "203.0.113.10:12345",
		"destination":    "198.51.100.20:443",
		"routeTarget":    nil,
		"originalTarget": nil,
		"inboundTag":     "inbound-1",
		"inboundName":    nil,
		"inboundLocal":   nil,
		"outboundTag":    "direct",
		"ts":             time.Now().UnixMilli(),
	}
	state.AddReport(report)
	service := plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), nil)
	return encodeEnvelope(service.CollectReports())
}

func testPluginBlockIPsResponseShape(t *testing.T) []byte {
	service := pluginService()
	return encodeEnvelope(service.BlockIPs([]plugin.BlockIP{{IP: "203.0.113.10", Timeout: 60}}))
}

func pluginService() *plugin.Service {
	state := plugin.NewState()
	return plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), nil)
}

func testPluginUnblockIPsResponseShape(t *testing.T) []byte {
	service := pluginService()
	return encodeEnvelope(service.UnblockIPs([]string{"203.0.113.10"}))
}

func testPluginRecreateTablesResponseShape(t *testing.T) []byte {
	service := pluginService()
	return encodeEnvelope(service.RecreateTables())
}

type stubStatsProvider struct{}

func (stubStatsProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	return &xtls.SysStats{NumGoroutine: 1, Uptime: 10}, nil
}
func (stubStatsProvider) GetAllUsersStats(context.Context, bool) ([]xtls.UserTraffic, error) {
	return []xtls.UserTraffic{{Username: "u1", Uplink: 1, Downlink: 2}}, nil
}
func (stubStatsProvider) GetUserOnlineStatus(context.Context, string) (bool, error) {
	return false, nil
}
func (stubStatsProvider) GetInboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{Tag: "in-1"}, nil
}
func (stubStatsProvider) GetOutboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{Tag: "out-1"}, nil
}
func (stubStatsProvider) GetAllInboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return []xtls.TagTraffic{{Tag: "in-1"}}, nil
}
func (stubStatsProvider) GetAllOutboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return []xtls.TagTraffic{{Tag: "out-1"}}, nil
}
func (stubStatsProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	return []xtls.IPEntry{{IP: "203.0.113.10", LastSeen: time.Now()}}, nil
}
func (stubStatsProvider) GetUsersIPList(context.Context) ([]xtls.UserIPEntry, error) {
	return []xtls.UserIPEntry{{
		UserID: "user-1",
		IPs:    []xtls.IPEntry{{IP: "203.0.113.10", LastSeen: time.Now()}},
	}}, nil
}

type stubReportsCounter struct{}

func (stubReportsCounter) ReportsCount() int { return 0 }
