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
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorCode"] != "A009" {
		t.Fatalf("errorCode = %v, want A009", body["errorCode"])
	}
}

func TestHandleGetUserIPListGRPCError(t *testing.T) {
	service := stats.NewService(mockProvider{usersErr: errors.New("grpc down")}, nil)
	req := httptest.NewRequest(http.MethodPost, "/node/stats/get-user-ip-list", strings.NewReader(`{"userId":"u1"}`))
	rec := httptest.NewRecorder()
	service.HandleGetUserIPList(rec, req, writeTestJSON)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["errorCode"] != "A009" {
		t.Fatalf("errorCode = %v, want A009", body["errorCode"])
	}
}
