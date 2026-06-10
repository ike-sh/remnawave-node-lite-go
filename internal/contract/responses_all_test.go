package contract_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/nodehandler"
	"remnawave-node-lite-go/internal/plugin"
	"remnawave-node-lite-go/internal/stats"
	"remnawave-node-lite-go/internal/vision"
	"remnawave-node-lite-go/internal/xray"
	"remnawave-node-lite-go/internal/xtls"
)

var responseShapeTests = map[string]func(t *testing.T){
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
	"/vision/block-ip":                      testVisionBlockIPResponseShape,
	"/vision/unblock-ip":                    testVisionUnblockIPResponseShape,
}

func TestOfficialResponseShapes(t *testing.T) {
	for _, route := range officialRoutes {
		route := route
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			fn, ok := responseShapeTests[route]
			if !ok {
				t.Fatalf("missing response shape test for %s", route)
			}
			fn(t)
		})
	}
}

func testManager(t *testing.T) *xray.Manager {
	t.Helper()
	manager, err := xray.NewManager(xray.Options{
		XrayBin:            "definitely-missing-rw-core",
		GeoDir:             t.TempDir(),
		LogDir:             t.TempDir(),
		InternalSocketPath: "/run/remnawave.sock",
		InternalRESTToken:  "token",
		XtlsAPIPort:        61000,
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

func testXrayStartResponseShape(t *testing.T) {
	manager := testManager(t)
	resp := manager.Start(context.Background(), xray.StartRequest{
		XrayConfig: map[string]any{"inbounds": []any{}},
	})
	raw := encodeEnvelope(resp)
	assertTopLevelResponse(t, raw)
	assertJSONPath(t, raw, "response.isStarted")
	assertJSONPath(t, raw, "response.nodeInformation.version")
	assertJSONPath(t, raw, "response.system.info.arch")
	assertJSONPath(t, raw, "response.system.stats.memoryFree")
}

func testXrayStopResponseShape(t *testing.T) {
	manager := testManager(t)
	raw := encodeEnvelope(manager.Stop(true))
	assertJSONPath(t, raw, "response.isStopped")
}

func testXrayHealthcheckResponseShape(t *testing.T) {
	manager := testManager(t)
	raw := encodeEnvelope(manager.Health())
	assertJSONPath(t, raw, "response.isAlive")
	assertJSONPath(t, raw, "response.xrayInternalStatusCached")
	assertJSONPath(t, raw, "response.xrayVersion")
	assertJSONPath(t, raw, "response.nodeVersion")
}

func statsService(t *testing.T) *stats.Service {
	t.Helper()
	return stats.NewService(stubStatsProvider{}, stubReportsCounter{})
}

func testGetUserOnlineStatusResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-user-online-status", strings.NewReader(`{"username":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetUserOnlineStatus(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.isOnline")
}

func testGetSystemStatsResponseShape(t *testing.T) {
	service := statsService(t)
	rec := httptest.NewRecorder()
	service.HandleGetSystemStats(rec, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.plugins.torrentBlocker.reportsCount")
	assertJSONPath(t, raw, "response.system.stats.memoryFree")
	assertJSONPath(t, raw, "response.system.stats.loadAvg")
}

func testGetUsersStatsResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetUsersStats(rec, req, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.users")
}

func testGetInboundStatsResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-inbound-stats", strings.NewReader(`{"tag":"in-1","reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetInboundStats(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.inbound")
	assertJSONPath(t, raw, "response.downlink")
	assertJSONPath(t, raw, "response.uplink")
}

func testGetOutboundStatsResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-outbound-stats", strings.NewReader(`{"tag":"out-1","reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetOutboundStats(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.outbound")
	assertJSONPath(t, raw, "response.downlink")
	assertJSONPath(t, raw, "response.uplink")
}

func testGetAllInboundsStatsResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-all-inbounds-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetAllInboundsStats(rec, req, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.inbounds")
}

func testGetAllOutboundsStatsResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-all-outbounds-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetAllOutboundsStats(rec, req, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.outbounds")
}

func testGetCombinedStatsResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-combined-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetCombinedStats(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPathArray(t, raw, "response.inbounds")
	assertJSONPathArray(t, raw, "response.outbounds")
}

func testGetUserIPListResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-user-ip-list", strings.NewReader(`{"userId":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetUserIPList(rec, req, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.ips")
}

func testGetUsersIPListResponseShape(t *testing.T) {
	service := statsService(t)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-ip-list", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	service.HandleGetUsersIPList(rec, req, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.users")
}

func handlerService() *nodehandler.Service {
	return nodehandler.NewService(stubHandlerProvider{}, connections.NewDropper(nil))
}

func testRemoveUserResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/remove-user", strings.NewReader(`{
		"username":"u1",
		"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}
	}`))
	rec := httptest.NewRecorder()
	service.HandleRemoveUser(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.success")
	assertJSONPath(t, raw, "response.error")
}

func testGetInboundUsersResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/get-inbound-users", strings.NewReader(`{"tag":"in-1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetInboundUsers(rec, req, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.users")
}

func testAddUsersResponseShape(t *testing.T) {
	service := handlerService()
	body := `{
		"data":[{"type":"vless","tag":"in-1","username":"u1","uuid":"00000000-0000-4000-8000-000000000001","flow":""}],
		"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000002"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/node/handler/add-users", strings.NewReader(body))
	rec := httptest.NewRecorder()
	service.HandleAddUsers(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.success")
	assertJSONPath(t, raw, "response.error")
}

func testRemoveUsersResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/remove-users", strings.NewReader(`{
		"usernames":["u1"],
		"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}
	}`))
	rec := httptest.NewRecorder()
	service.HandleRemoveUsers(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.success")
	assertJSONPath(t, raw, "response.error")
}

func testDropIPsResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/drop-ips", strings.NewReader(`{"ips":["203.0.113.10"]}`))
	rec := httptest.NewRecorder()
	service.HandleDropIPs(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.success")
}

func testAddUserResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/add-user", strings.NewReader(`{
		"data":[{"type":"vless","tag":"in-1","username":"u1","uuid":"00000000-0000-4000-8000-000000000001","flow":""}],
		"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000002"}
	}`))
	rec := httptest.NewRecorder()
	service.HandleAddUser(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.success")
	assertJSONPath(t, raw, "response.error")
}

func testDropUsersConnectionsResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/drop-users-connections", strings.NewReader(`{"userIds":["user-1"]}`))
	rec := httptest.NewRecorder()
	service.HandleDropUsersConnections(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.success")
}

func testGetInboundUsersCountResponseShape(t *testing.T) {
	service := handlerService()
	req := httptest.NewRequest(http.MethodPost, "/node/handler/get-inbound-users-count", strings.NewReader(`{"tag":"in-1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetInboundUsersCount(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.count")
}

func testPluginSyncResponseShape(t *testing.T) {
	service := pluginService()
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", strings.NewReader(`{"plugin":null}`))
	rec := httptest.NewRecorder()
	service.HandleSync(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.accepted")
}

func testPluginCollectReportsResponseShape(t *testing.T) {
	service := pluginService()
	rec := httptest.NewRecorder()
	service.HandleCollectReports(rec, writeTestJSON)
	assertJSONPathArray(t, rec.Body.Bytes(), "response.reports")
}

func testPluginBlockIPsResponseShape(t *testing.T) {
	service := pluginService()
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/nftables/block-ips", strings.NewReader(`{"ips":[{"ip":"203.0.113.10","timeout":60}]}`))
	rec := httptest.NewRecorder()
	service.HandleBlockIPs(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.accepted")
}

func pluginService() *plugin.Service {
	state := plugin.NewState()
	return plugin.NewService(state, connections.NewDropper(state.IsWhitelisted), nil)
}

func testPluginUnblockIPsResponseShape(t *testing.T) {
	service := pluginService()
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/nftables/unblock-ips", strings.NewReader(`{"ips":["203.0.113.10"]}`))
	rec := httptest.NewRecorder()
	service.HandleUnblockIPs(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.accepted")
}

func testPluginRecreateTablesResponseShape(t *testing.T) {
	service := pluginService()
	req := httptest.NewRequest(http.MethodPost, "/node/plugin/nftables/recreate-tables", nil)
	rec := httptest.NewRecorder()
	service.HandleRecreateTables(rec, req, writeTestJSON)
	assertJSONPath(t, rec.Body.Bytes(), "response.accepted")
}

func testVisionBlockIPResponseShape(t *testing.T) {
	service := vision.NewService(nil)
	req := httptest.NewRequest(http.MethodPost, "/vision/block-ip", strings.NewReader(`{"ip":"203.0.113.10","username":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleBlockIP(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.success")
	assertJSONPath(t, raw, "response.error")
}

func testVisionUnblockIPResponseShape(t *testing.T) {
	service := vision.NewService(nil)
	req := httptest.NewRequest(http.MethodPost, "/vision/unblock-ip", strings.NewReader(`{"ip":"203.0.113.10","username":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleUnblockIP(rec, req, writeTestJSON)
	raw := rec.Body.Bytes()
	assertJSONPath(t, raw, "response.success")
	assertJSONPath(t, raw, "response.error")
}

type stubStatsProvider struct{}

func (stubStatsProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	return &xtls.SysStats{NumGoroutine: 1, Uptime: 10}, nil
}
func (stubStatsProvider) GetAllUsersStats(context.Context, bool) ([]xtls.UserTraffic, error) {
	return []xtls.UserTraffic{{Username: "u1", Uplink: 1, Downlink: 2}}, nil
}
func (stubStatsProvider) GetUserOnlineStatus(context.Context, string) (bool, error) { return false, nil }
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
	return []xtls.UserIPEntry{{UserID: "u1"}}, nil
}

type stubReportsCounter struct{}

func (stubReportsCounter) ReportsCount() int { return 0 }
