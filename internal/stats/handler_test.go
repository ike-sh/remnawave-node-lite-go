package stats_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remnawave-node-lite-go/internal/stats"
	"remnawave-node-lite-go/internal/xtls"
)

type mockProvider struct {
	usersStats []xtls.UserTraffic
	usersErr   error
}

type offlineSysStatsProvider struct {
	mockProvider
}

func (offlineSysStatsProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	return nil, errors.New("xray is not online")
}

func (m mockProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	return &xtls.SysStats{Uptime: 1}, nil
}
func (m mockProvider) GetAllUsersStats(context.Context, bool) ([]xtls.UserTraffic, error) {
	return m.usersStats, m.usersErr
}
func (m mockProvider) GetUserOnlineStatus(context.Context, string) (bool, error) {
	return false, m.usersErr
}
func (m mockProvider) GetInboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{}, nil
}
func (m mockProvider) GetOutboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{}, nil
}
func (m mockProvider) GetAllInboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return nil, nil
}
func (m mockProvider) GetAllOutboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return nil, nil
}
func (m mockProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	return nil, m.usersErr
}
func (m mockProvider) GetUsersIPList(context.Context) ([]xtls.UserIPEntry, error) {
	return nil, m.usersErr
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestHandleGetSystemStatsReturnsErrorWhenOffline(t *testing.T) {
	service := stats.NewService(offlineSysStatsProvider{}, nil)
	rec := httptest.NewRecorder()
	service.HandleGetSystemStats(rec, writeTestJSON)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when rw-core offline", rec.Code)
	}
	var body struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorCode != "A010" {
		t.Fatalf("errorCode = %q, want A010", body.ErrorCode)
	}
}

func TestHandleGetUsersStatsFiltersZeroTraffic(t *testing.T) {
	service := stats.NewService(mockProvider{usersStats: []xtls.UserTraffic{
		{Username: "idle", Uplink: 0, Downlink: 0},
		{Username: "active", Uplink: 10, Downlink: 5},
	}}, nil)

	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetUsersStats(rec, req, writeTestJSON)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Response struct {
			Users []struct {
				Username string `json:"username"`
			} `json:"users"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Response.Users) != 1 || body.Response.Users[0].Username != "active" {
		t.Fatalf("users = %+v, want only active user", body.Response.Users)
	}
}

func TestHandleGetUsersStatsGRPCError(t *testing.T) {
	service := stats.NewService(mockProvider{usersErr: errors.New("grpc down")}, nil)

	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", strings.NewReader(`{"reset":false}`))
	rec := httptest.NewRecorder()
	service.HandleGetUsersStats(rec, req, writeTestJSON)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["errorCode"] != "A011" {
		t.Fatalf("errorCode = %v, want A011", body["errorCode"])
	}
}

func TestHandleGetUserOnlineStatusGRPCError(t *testing.T) {
	service := stats.NewService(mockProvider{usersErr: errors.New("grpc down")}, nil)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-user-online-status", strings.NewReader(`{"username":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetUserOnlineStatus(rec, req, writeTestJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Response struct {
			IsOnline bool `json:"isOnline"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Response.IsOnline {
		t.Fatal("expected isOnline=false when provider returns error")
	}
}

func TestHandleGetUserIPListGRPCError(t *testing.T) {
	service := stats.NewService(mockProvider{usersErr: errors.New("grpc down")}, nil)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-user-ip-list", strings.NewReader(`{"userId":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetUserIPList(rec, req, writeTestJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty ips", rec.Code)
	}
	var body struct {
		Response struct {
			IPs []any `json:"ips"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Response.IPs) != 0 {
		t.Fatalf("ips = %+v, want empty", body.Response.IPs)
	}
}

func TestHandleGetUsersIPListGRPCError(t *testing.T) {
	service := stats.NewService(mockProvider{usersErr: errors.New("grpc down")}, nil)
	req := httptest.NewRequest(http.MethodGet, "/node/stats/get-users-ip-list", nil)
	rec := httptest.NewRecorder()
	service.HandleGetUsersIPList(rec, req, writeTestJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with empty users", rec.Code)
	}
	var body struct {
		Response struct {
			Users []any `json:"users"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Response.Users) != 0 {
		t.Fatalf("users = %+v, want empty", body.Response.Users)
	}
}
