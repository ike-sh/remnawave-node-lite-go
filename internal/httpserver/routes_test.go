package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remnawave-node-lite-go/internal/plugin"
	"remnawave-node-lite-go/internal/stats"
	"remnawave-node-lite-go/internal/xray"
	"remnawave-node-lite-go/internal/xtls"
)

type failingUsersStatsProvider struct{}

func (failingUsersStatsProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	return &xtls.SysStats{}, nil
}
func (f failingUsersStatsProvider) GetAllUsersStats(context.Context, bool) ([]xtls.UserTraffic, error) {
	return nil, errors.New("grpc unavailable")
}
func (f failingUsersStatsProvider) GetUserOnlineStatus(context.Context, string) (bool, error) {
	return false, nil
}
func (f failingUsersStatsProvider) GetInboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{}, nil
}
func (f failingUsersStatsProvider) GetOutboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{}, nil
}
func (f failingUsersStatsProvider) GetAllInboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return nil, nil
}
func (f failingUsersStatsProvider) GetAllOutboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return nil, nil
}
func (f failingUsersStatsProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	return nil, nil
}
func (f failingUsersStatsProvider) GetUsersIPList(context.Context) ([]xtls.UserIPEntry, error) {
	return nil, nil
}

func TestHandleNodeRoutesUsersStatsError(t *testing.T) {
	t.Parallel()

	server := &Server{
		statsService: stats.NewService(failingUsersStatsProvider{}, nil),
	}
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()

	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorCode"] != "A011" {
		t.Fatalf("errorCode = %v, want A011", body["errorCode"])
	}
	if body["path"] != "/node/stats/get-users-stats" {
		t.Fatalf("error path = %v", body["path"])
	}
}

func TestHandleNodeRoutesUnknownPath(t *testing.T) {
	t.Parallel()

	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/node/unknown", nil)
	rec := httptest.NewRecorder()

	expectRequestAbort(t, func() { server.handleNodeRoutes(rec, req) })
}

func expectRequestAbort(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("abort = %v, want http.ErrAbortHandler", got)
		}
	}()
	call()
}

func TestHandleNodeRoutesRetiredMethods(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/node/handler/get-inbound-users-count"},
		{http.MethodPost, "/node/handler/get-inbound-users"},
		{http.MethodPost, "/node/xray/stop"},
	} {
		rec := httptest.NewRecorder()
		expectRequestAbort(t, func() { (&Server{}).handleNodeRoutes(rec, httptest.NewRequest(route.method, route.path, nil)) })
	}
}

func TestHandleNodeRoutesGeocheck(t *testing.T) {
	t.Parallel()

	server := &Server{statsService: stats.NewService(nil, nil, "geocheck")}
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-geocheck", strings.NewReader(`{"ip":"invalid"}`))
	rec := httptest.NewRecorder()
	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorCode"] != "A018" {
		t.Fatalf("errorCode = %v, want A018", body["errorCode"])
	}
}

func TestStartRejectsMalformedAndMissingConfig(t *testing.T) {
	server := &Server{}
	for _, body := range []string{"{", `{"internals":{"hashes":{"emptyConfig":"","inbounds":[]}}}`} {
		rec := httptest.NewRecorder()
		server.handleNodeRoutes(rec, httptest.NewRequest(http.MethodPost, "/node/xray/start", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d", body, rec.Code)
		}
	}
}

func TestStartXrayFailureReturnsBusinessError(t *testing.T) {
	manager, err := xray.NewManager(xray.Options{XrayBin: "missing-rw-core", GeoDir: t.TempDir(), LogDir: t.TempDir(), DataDir: t.TempDir(), InternalSocketPath: "/run/test.sock", InternalRESTToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{manager: manager}
	rec := httptest.NewRecorder()
	body := `{"xrayConfig":{},"internals":{"hashes":{"emptyConfig":"","inbounds":[]}}}`
	server.handleNodeRoutes(rec, httptest.NewRequest(http.MethodPost, "/node/xray/start", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	var result struct {
		Response struct {
			IsStarted bool    `json:"isStarted"`
			Error     *string `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Response.IsStarted || result.Response.Error == nil {
		t.Fatalf("Xray failure response = %s", rec.Body.String())
	}
}

func TestInvalidPluginConfigurationReturnsRejectedSync(t *testing.T) {
	state := plugin.NewState()
	server := &Server{pluginService: plugin.NewService(state, nil, nil)}
	body := `{"plugin":{"uuid":"00000000-0000-4000-8000-000000000001","name":"test","config":{"sharedLists":"invalid"}}}`
	rec := httptest.NewRecorder()
	server.handleNodeRoutes(rec, httptest.NewRequest(http.MethodPost, "/node/plugin/sync", strings.NewReader(body)))
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"accepted":false`) {
		t.Fatalf("plugin response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidPostBodiesUseOfficialErrorEnvelope(t *testing.T) {
	cases := []struct {
		name, path, body string
		malformed        bool
	}{
		{"malformed", "/node/stats/get-users-stats", "{", true},
		{"empty", "/node/stats/get-users-stats", "", false},
		{"wrong type", "/node/stats/get-users-stats", `{"reset":"invalid"}`, false},
		{"missing field", "/node/stats/get-users-stats", `{}`, false},
		{"bad uuid", "/node/handler/remove-user", `{"username":"u","hashData":{"vlessUuid":"bad"}}`, false},
		{"bad enum", "/node/handler/add-user", `{"data":[{"type":"unknown","tag":"x","username":"u"}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`, false},
		{"bad ip", "/node/plugin/nftables/block-ips", `{"ips":[{"ip":"999.0.0.1","timeout":10}]}`, false},
		{"plugin missing config", "/node/plugin/sync", `{"plugin":{"uuid":"00000000-0000-4000-8000-000000000001","name":"compat"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			(&Server{}).handleNodeRoutes(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["statusCode"] != float64(400) {
				t.Fatalf("missing statusCode: %v", body)
			}
			if tc.malformed {
				if body["error"] != "Bad Request" {
					t.Fatalf("malformed JSON body = %v", body)
				}
			} else if issues, ok := body["errors"].([]any); !ok || len(issues) == 0 {
				t.Fatalf("missing validation issues: %v", body)
			}
		})
	}
}

func TestPostSuccessUsesOfficialCreatedStatus(t *testing.T) {
	server := &Server{statsService: stats.NewService(failingUsersStatsProvider{}, nil)}
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-user-online-status", strings.NewReader(`{"username":"nobody","extra":"ignored"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.handleNodeRoutes(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"isOnline":false`) {
		t.Fatalf("response = %s", rec.Body.String())
	}
}
