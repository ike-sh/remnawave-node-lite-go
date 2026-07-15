package xtls

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls/rwstats"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeInvokeConn struct {
	calls  atomic.Int64
	invoke func(context.Context, string, any, any, ...grpc.CallOption) error
}

func (c *fakeInvokeConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	c.calls.Add(1)
	return c.invoke(ctx, method, args, reply, opts...)
}

func (*fakeInvokeConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, errors.New("streams are not used")
}

func TestGetUsersIPListUsesNativeBatchRPC(t *testing.T) {
	t.Parallel()

	conn := &fakeInvokeConn{invoke: func(_ context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
		if method != getUsersStatsMethod {
			return fmt.Errorf("method = %q", method)
		}
		request, ok := args.(*rwstats.GetUsersStatsRequest)
		if !ok || request.GetIncludeTraffic() || !request.GetReset_() {
			return fmt.Errorf("unexpected request: %#v", args)
		}
		response := reply.(*rwstats.GetUsersStatsResponse)
		response.Users = []*rwstats.UserStat{
			{
				Email: "u1",
				Ips: []*rwstats.OnlineIPEntry{{
					Ip: "203.0.113.10", LastSeen: 1_700_000_000,
				}},
			},
			{Email: "u2", Ips: []*rwstats.OnlineIPEntry{}},
		}
		return nil
	}}
	capabilities := &StatsCapabilities{}
	api := NewStatsAPI(conn, capabilities)

	users, err := api.GetUsersIPList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if conn.calls.Load() != 1 {
		t.Fatalf("native calls = %d, want one", conn.calls.Load())
	}
	if len(users) != 2 || users[0].UserID != "u1" || users[1].UserID != "u2" {
		t.Fatalf("users = %#v, want native response including empty IP user", users)
	}
	if len(users[0].IPs) != 1 || users[0].IPs[0].IP != "203.0.113.10" ||
		!users[0].IPs[0].LastSeen.Equal(time.Unix(1_700_000_000, 0).UTC()) {
		t.Fatalf("unexpected native IP mapping: %#v", users[0].IPs)
	}
	if capabilities.usersStats.Load() != usersStatsSupported {
		t.Fatal("native capability was not cached as supported")
	}
}

type fakeLegacyStatsClient struct {
	statscommand.StatsServiceClient
	onlineUsers []string
	lookupErr   error
	allCalls    atomic.Int64
	lookupCalls atomic.Int64
	active      atomic.Int64
	maxActive   atomic.Int64
}

func (c *fakeLegacyStatsClient) GetAllOnlineUsers(context.Context, *statscommand.GetAllOnlineUsersRequest, ...grpc.CallOption) (*statscommand.GetAllOnlineUsersResponse, error) {
	c.allCalls.Add(1)
	return &statscommand.GetAllOnlineUsersResponse{Users: c.onlineUsers}, nil
}

func (c *fakeLegacyStatsClient) GetStatsOnlineIpList(ctx context.Context, request *statscommand.GetStatsRequest, _ ...grpc.CallOption) (*statscommand.GetStatsOnlineIpListResponse, error) {
	c.lookupCalls.Add(1)
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for {
		maximum := c.maxActive.Load()
		if active <= maximum || c.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(2 * time.Millisecond):
	}
	if c.lookupErr != nil {
		return nil, c.lookupErr
	}
	return &statscommand.GetStatsOnlineIpListResponse{
		Name: request.GetName(),
		Ips:  map[string]int64{"203.0.113.10": 1_700_000_000},
	}, nil
}

func TestGetUsersIPListLegacyUsesFixedWorkerCount(t *testing.T) {
	t.Parallel()

	metrics := make([]string, 100)
	for index := range metrics {
		metrics[index] = fmt.Sprintf("user>>>u-%03d>>>online", index)
	}
	client := &fakeLegacyStatsClient{onlineUsers: metrics}
	capabilities := &StatsCapabilities{}
	capabilities.usersStats.Store(usersStatsLegacy)
	api := &StatsAPI{client: client, capabilities: capabilities}

	users, err := api.GetUsersIPList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != len(metrics) || client.lookupCalls.Load() != int64(len(metrics)) {
		t.Fatalf("users=%d lookups=%d, want %d", len(users), client.lookupCalls.Load(), len(metrics))
	}
	if maximum := client.maxActive.Load(); maximum < 2 || maximum > legacyIPLookupWorkers {
		t.Fatalf("maximum concurrent lookups = %d, want 2..%d", maximum, legacyIPLookupWorkers)
	}
}

func TestGetUsersIPListCachesLegacyCapabilityAcrossAPIs(t *testing.T) {
	t.Parallel()

	conn := &fakeInvokeConn{invoke: func(context.Context, string, any, any, ...grpc.CallOption) error {
		return status.Error(codes.Unimplemented, "unknown method GetUsersStats")
	}}
	capabilities := &StatsCapabilities{}
	for range 2 {
		api := NewStatsAPI(conn, capabilities)
		client := &fakeLegacyStatsClient{}
		api.client = client
		if _, err := api.GetUsersIPList(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if conn.calls.Load() != 1 {
		t.Fatalf("native capability probes = %d, want one", conn.calls.Load())
	}
	if capabilities.usersStats.Load() != usersStatsLegacy {
		t.Fatal("legacy capability was not cached")
	}
}

func TestGetUsersIPListDoesNotFallbackOnNativeFailure(t *testing.T) {
	t.Parallel()

	conn := &fakeInvokeConn{invoke: func(context.Context, string, any, any, ...grpc.CallOption) error {
		return status.Error(codes.Unavailable, "core unavailable")
	}}
	client := &fakeLegacyStatsClient{}
	api := NewStatsAPI(conn, nil)
	api.client = client
	if _, err := api.GetUsersIPList(context.Background()); status.Code(err) != codes.Unavailable {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if client.allCalls.Load() != 0 {
		t.Fatalf("legacy calls = %d, want zero for non-UNIMPLEMENTED failure", client.allCalls.Load())
	}
}

func TestGetUsersIPListLegacyPropagatesCancellation(t *testing.T) {
	t.Parallel()

	client := &fakeLegacyStatsClient{onlineUsers: []string{"user>>>u1>>>online"}}
	capabilities := &StatsCapabilities{}
	capabilities.usersStats.Store(usersStatsLegacy)
	api := &StatsAPI{client: client, capabilities: capabilities}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := api.GetUsersIPList(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestExtractOnlineUserID(t *testing.T) {
	if got := extractOnlineUserID("user>>>alice@example.com>>>online"); got != "alice@example.com" {
		t.Fatalf("unexpected user id: %q", got)
	}
	if got := extractOnlineUserID("invalid"); got != "" {
		t.Fatalf("expected empty for invalid metric")
	}
}

func TestUniqueOnlineUserIDs(t *testing.T) {
	users := uniqueOnlineUserIDs([]string{
		"user>>>a>>>online",
		"user>>>a>>>online",
		"user>>>b>>>online",
	})
	if len(users) != 2 || users[0] != "a" || users[1] != "b" {
		t.Fatalf("unexpected users: %#v", users)
	}
}

func TestParseUserTrafficStats(t *testing.T) {
	stats := []*statscommand.Stat{
		{Name: "user>>>alice@example.com>>>traffic>>>uplink", Value: 100},
		{Name: "user>>>alice@example.com>>>traffic>>>downlink", Value: 200},
	}
	users := parseUserTrafficStats(stats)
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Username != "alice@example.com" || users[0].Uplink != 100 || users[0].Downlink != 200 {
		t.Fatalf("unexpected user stats: %#v", users[0])
	}
}

func TestParseAllTagTraffic(t *testing.T) {
	stats := []*statscommand.Stat{
		{Name: "inbound>>>vless-in>>>traffic>>>uplink", Value: 10},
		{Name: "inbound>>>vless-in>>>traffic>>>downlink", Value: 20},
	}
	items := parseAllTagTraffic(stats, "inbound")
	if len(items) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(items))
	}
	if items[0].Tag != "vless-in" || items[0].Uplink != 10 || items[0].Downlink != 20 {
		t.Fatalf("unexpected inbound stats: %#v", items[0])
	}
}
