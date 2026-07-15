package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/stats"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
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
		t.Fatalf("path = %v, want request path", body["path"])
	}
	if body["timestamp"] == nil {
		t.Fatal("timestamp is missing")
	}
	if err := contractspec.OfficialErrors.ApplicationResponse.ValidateJSON(rec.Body.Bytes()); err != nil {
		t.Fatalf("application error violates official schema: %v\n%s", err, rec.Body.Bytes())
	}
}

type countingStatsProvider struct {
	calls *atomic.Int64
}

func (p countingStatsProvider) hit() { p.calls.Add(1) }

func (p countingStatsProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	p.hit()
	return &xtls.SysStats{}, nil
}
func (p countingStatsProvider) GetAllUsersStats(context.Context, bool) ([]xtls.UserTraffic, error) {
	p.hit()
	return []xtls.UserTraffic{}, nil
}
func (p countingStatsProvider) GetUserOnlineStatus(context.Context, string) (bool, error) {
	p.hit()
	return false, nil
}
func (p countingStatsProvider) GetInboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	p.hit()
	return xtls.TagTraffic{Tag: "inbound"}, nil
}
func (p countingStatsProvider) GetOutboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	p.hit()
	return xtls.TagTraffic{Tag: "outbound"}, nil
}
func (p countingStatsProvider) GetAllInboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	p.hit()
	return []xtls.TagTraffic{}, nil
}
func (p countingStatsProvider) GetAllOutboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	p.hit()
	return []xtls.TagTraffic{}, nil
}
func (p countingStatsProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	p.hit()
	return []xtls.IPEntry{}, nil
}
func (p countingStatsProvider) GetUsersIPList(context.Context) ([]xtls.UserIPEntry, error) {
	p.hit()
	return []xtls.UserIPEntry{}, nil
}

func TestStatsValidationPrecedesProviderCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "online missing username", path: "/node/stats/get-user-online-status", body: `{}`},
		{name: "users missing reset", path: "/node/stats/get-users-stats", body: `{}`},
		{name: "inbound missing fields", path: "/node/stats/get-inbound-stats", body: `{}`},
		{name: "outbound missing fields", path: "/node/stats/get-outbound-stats", body: `{}`},
		{name: "all inbounds missing reset", path: "/node/stats/get-all-inbounds-stats", body: `{}`},
		{name: "all outbounds missing reset", path: "/node/stats/get-all-outbounds-stats", body: `{}`},
		{name: "combined missing reset", path: "/node/stats/get-combined-stats", body: `{}`},
		{name: "user IP missing user ID", path: "/node/stats/get-user-ip-list", body: `{}`},
		{name: "malformed", path: "/node/stats/get-users-stats", body: `{"reset":`},
		{name: "wrong type", path: "/node/stats/get-users-stats", body: `{"reset":"false"}`},
		{name: "trailing document", path: "/node/stats/get-users-stats", body: `{"reset":false}{"reset":true}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			rec := httptest.NewRecorder()

			server.handleNodeRoutes(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatalf("provider calls = %d, want 0", calls.Load())
			}
			var body struct {
				StatusCode int              `json:"statusCode"`
				Message    string           `json:"message"`
				Errors     []map[string]any `json:"errors"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.StatusCode != 400 || body.Message != "Validation failed" || len(body.Errors) == 0 {
				t.Fatalf("validation response = %+v", body)
			}
		})
	}
}

func TestStatsRequestAllowsUnknownFieldsAndEmptyStrings(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
	req := httptest.NewRequest(
		http.MethodPost,
		"/node/stats/get-user-online-status",
		strings.NewReader(`{"username":"","ignored":true}`),
	)
	rec := httptest.NewRecorder()

	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
}

func TestStatsRoutesProduceOfficialResponseShapes(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/node/stats/get-user-online-status",
		"/node/stats/get-system-stats",
		"/node/stats/get-users-stats",
		"/node/stats/get-inbound-stats",
		"/node/stats/get-outbound-stats",
		"/node/stats/get-all-inbounds-stats",
		"/node/stats/get-all-outbounds-stats",
		"/node/stats/get-combined-stats",
		"/node/stats/get-user-ip-list",
		"/node/stats/get-users-ip-list",
	}

	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			route, ok := contractspec.FindRouteByPath(path)
			if !ok {
				t.Fatalf("contract route %s is missing", path)
			}
			var calls atomic.Int64
			server := &Server{statsService: stats.NewService(countingStatsProvider{calls: &calls}, nil)}
			req := httptest.NewRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest))
			rec := httptest.NewRecorder()

			server.handleNodeRoutes(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if err := contractspec.ValidateResponse(path, rec.Body.Bytes()); err != nil {
				t.Fatalf("response violates official schema: %v\n%s", err, rec.Body.Bytes())
			}
		})
	}
}

func TestHandleNodeRoutesUnknownPath(t *testing.T) {
	t.Parallel()

	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/node/unknown", nil)
	rec := httptest.NewRecorder()

	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleNodeRoutesRejectsUnregisteredMethod(t *testing.T) {
	t.Parallel()

	server := &Server{}
	for _, route := range RegisteredNodeRoutes() {
		wrongMethod := http.MethodGet
		if route.Method == http.MethodGet {
			wrongMethod = http.MethodPost
		}
		req := httptest.NewRequest(wrongMethod, route.Path, nil)
		rec := httptest.NewRecorder()

		server.handleNodeRoutes(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", wrongMethod, route.Path, rec.Code)
		}
	}
}
