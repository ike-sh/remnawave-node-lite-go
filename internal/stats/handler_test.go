package stats_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/stats"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

type mockProvider struct {
	usersStats       []xtls.UserTraffic
	usersIPList      []xtls.UserIPEntry
	usersErr         error
	onlineCalls      int
	userIPListCalls  int
	onlineUsername   string
	userIPListUserID string
}

func (m *mockProvider) GetSysStats(context.Context) (*xtls.SysStats, error) {
	return &xtls.SysStats{Uptime: 1}, m.usersErr
}
func (m *mockProvider) GetAllUsersStats(context.Context, bool) ([]xtls.UserTraffic, error) {
	return m.usersStats, m.usersErr
}
func (m *mockProvider) GetUserOnlineStatus(_ context.Context, username string) (bool, error) {
	m.onlineCalls++
	m.onlineUsername = username
	return false, m.usersErr
}
func (m *mockProvider) GetInboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{}, m.usersErr
}
func (m *mockProvider) GetOutboundStats(context.Context, string, bool) (xtls.TagTraffic, error) {
	return xtls.TagTraffic{}, m.usersErr
}
func (m *mockProvider) GetAllInboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return nil, m.usersErr
}
func (m *mockProvider) GetAllOutboundsStats(context.Context, bool) ([]xtls.TagTraffic, error) {
	return nil, m.usersErr
}
func (m *mockProvider) GetUserIPList(_ context.Context, userID string, _ bool) ([]xtls.IPEntry, error) {
	m.userIPListCalls++
	m.userIPListUserID = userID
	return nil, m.usersErr
}
func (m *mockProvider) GetUsersIPList(context.Context) ([]xtls.UserIPEntry, error) {
	return m.usersIPList, m.usersErr
}

func TestGetSystemStatsReturnsErrorWhenOffline(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersErr: errors.New("xray is not online")}, nil)
	_, err := service.GetSystemStats(context.Background())
	assertServiceError(t, err, "A010")
}

func TestGetUsersStatsFiltersZeroTraffic(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersStats: []xtls.UserTraffic{
		{Username: "idle", Uplink: 0, Downlink: 0},
		{Username: "active", Uplink: 10, Downlink: 5},
	}}, nil)

	response, err := service.GetUsersStats(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Users) != 1 || response.Users[0].Username != "active" {
		t.Fatalf("users = %+v, want only active user", response.Users)
	}
}

func TestGetUsersStatsGRPCError(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersErr: errors.New("grpc down")}, nil)
	_, err := service.GetUsersStats(context.Background(), false)
	assertServiceError(t, err, "A011")
}

func TestGetUserOnlineStatusGRPCError(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersErr: errors.New("grpc down")}, nil)
	response := service.GetUserOnlineStatus(context.Background(), "u1")
	if response.IsOnline {
		t.Fatal("expected isOnline=false when provider returns error")
	}
}

func TestEmptyStringRequestsReachProvider(t *testing.T) {
	t.Parallel()

	provider := &mockProvider{}
	service := stats.NewService(provider, nil)
	service.GetUserOnlineStatus(context.Background(), "")
	service.GetUserIPList(context.Background(), "")

	if provider.onlineCalls != 1 || provider.userIPListCalls != 1 {
		t.Fatalf("provider calls online=%d userIPList=%d, want one each", provider.onlineCalls, provider.userIPListCalls)
	}
	if provider.onlineUsername != "" || provider.userIPListUserID != "" {
		t.Fatalf("provider received username=%q userID=%q, want empty strings", provider.onlineUsername, provider.userIPListUserID)
	}
}

func TestGetUserIPListGRPCError(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersErr: errors.New("grpc down")}, nil)
	response := service.GetUserIPList(context.Background(), "u1")
	if len(response.IPs) != 0 {
		t.Fatalf("ips = %+v, want empty", response.IPs)
	}
}

func TestGetUsersIPListGRPCError(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersErr: errors.New("grpc down")}, nil)
	response := service.GetUsersIPList(context.Background())
	if len(response.Users) != 0 {
		t.Fatalf("users = %+v, want empty", response.Users)
	}
}

func TestGetUsersIPListPreservesNativeUsersWithEmptyIPs(t *testing.T) {
	t.Parallel()

	service := stats.NewService(&mockProvider{usersIPList: []xtls.UserIPEntry{
		{UserID: "u1", IPs: []xtls.IPEntry{}},
	}}, nil)
	response := service.GetUsersIPList(context.Background())
	if len(response.Users) != 1 || response.Users[0].UserID != "u1" || len(response.Users[0].IPs) != 0 {
		t.Fatalf("users = %+v, want native empty-IP entry preserved", response.Users)
	}
}

func assertServiceError(t *testing.T, err error, code string) {
	t.Helper()
	serviceError, ok := nodeapi.AsServiceError(err)
	if !ok {
		t.Fatalf("error = %v, want ServiceError", err)
	}
	if serviceError.Code != code {
		t.Fatalf("error code = %q, want %q", serviceError.Code, code)
	}
}
