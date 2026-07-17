package xtls

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls/xrpc"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestHandlerAccountWireGoldens(t *testing.T) {
	tests := []struct {
		name      string
		wantType  string
		wantValue string
		add       func(*HandlerAPI) HandlerResult
	}{
		{
			// Captured from @remnawave/xtls-sdk 0.16.0's generated VLESS
			// Account encoder for {id:"id", flow:"flow", encryption:"none"}.
			name: "vless", wantType: vlessAccountType, wantValue: "0a0269641204666c6f771a046e6f6e65",
			add: func(api *HandlerAPI) HandlerResult {
				return api.AddVlessUser(context.Background(), "in", "user", "id", "flow", 3)
			},
		},
		{
			name: "trojan", wantType: trojanAccountType, wantValue: "0a06736563726574",
			add: func(api *HandlerAPI) HandlerResult {
				return api.AddTrojanUser(context.Background(), "in", "user", "secret", 3)
			},
		},
		{
			name: "shadowsocks", wantType: shadowsocksAccountType, wantValue: "0a0673656372657410061801",
			add: func(api *HandlerAPI) HandlerResult {
				return api.AddShadowsocksUser(context.Background(), "in", "user", "secret", 6, true, 3)
			},
		},
		{
			name: "shadowsocks-2022", wantType: shadowsocks2022AccountType, wantValue: "0a036b6579",
			add: func(api *HandlerAPI) HandlerResult {
				return api.AddShadowsocks2022User(context.Background(), "in", "user", "key", 3)
			},
		},
		{
			name: "hysteria", wantType: hysteriaAccountType, wantValue: "0a0461757468",
			add: func(api *HandlerAPI) HandlerResult {
				return api.AddHysteriaUser(context.Background(), "in", "user", "auth", 3)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := &fakeInvokeConn{invoke: func(_ context.Context, method string, request, _ any, _ ...grpc.CallOption) error {
				if method != handlerAlterInboundMethod {
					return fmt.Errorf("method = %q", method)
				}
				operation := request.(*xrpc.AlterInboundRequest).GetOperation()
				if operation.GetType() != addUserOperationType {
					return fmt.Errorf("operation type = %q", operation.GetType())
				}
				var add xrpc.AddUserOperation
				if err := proto.Unmarshal(operation.GetValue(), &add); err != nil {
					return err
				}
				account := add.GetUser().GetAccount()
				if account.GetType() != test.wantType || hex.EncodeToString(account.GetValue()) != test.wantValue {
					return fmt.Errorf("account = type %q value %x", account.GetType(), account.GetValue())
				}
				return nil
			}}
			if result := test.add(NewHandlerAPI(conn)); !result.OK {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestHandlerRequestWireGoldens(t *testing.T) {
	tests := []struct {
		name string
		want string
		call func(*HandlerAPI) HandlerResult
	}{
		{
			name: "add-vless",
			want: "0a02696e12660a2a787261792e6170702e70726f78796d616e2e636f6d6d616e642e416464557365724f7065726174696f6e12380a3608031204757365721a2c0a18787261792e70726f78792e766c6573732e4163636f756e7412100a0269641204666c6f771a046e6f6e65",
			call: func(api *HandlerAPI) HandlerResult {
				return api.AddVlessUser(context.Background(), "in", "user", "id", "flow", 3)
			},
		},
		{
			name: "remove",
			want: "0a02696e12370a2d787261792e6170702e70726f78796d616e2e636f6d6d616e642e52656d6f7665557365724f7065726174696f6e12060a0475736572",
			call: func(api *HandlerAPI) HandlerResult { return api.RemoveUser(context.Background(), "in", "user") },
		},
	}
	marshal := proto.MarshalOptions{Deterministic: true}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := &fakeInvokeConn{invoke: func(_ context.Context, _ string, request, _ any, _ ...grpc.CallOption) error {
				raw, err := marshal.Marshal(request.(proto.Message))
				if err != nil {
					return err
				}
				if got := hex.EncodeToString(raw); got != test.want {
					return fmt.Errorf("wire = %s, want %s", got, test.want)
				}
				return nil
			}}
			if result := test.call(NewHandlerAPI(conn)); !result.OK {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestStatsWireGoldens(t *testing.T) {
	marshal := proto.MarshalOptions{Deterministic: true}
	tests := []struct {
		name    string
		message proto.Message
		want    string
	}{
		{"query request", &xrpc.QueryStatsRequest{Pattern: "user>>>", Reset_: true}, "0a07757365723e3e3e1001"},
		{"query response", &xrpc.QueryStatsResponse{Stat: []*xrpc.Stat{{Name: "metric", Value: 42}}}, "0a0a0a066d6574726963102a"},
		{"sys response", &xrpc.SysStatsResponse{NumGoroutine: 1, NumGc: 2, Alloc: 3, TotalAlloc: 4, Sys: 5, Mallocs: 6, Frees: 7, LiveObjects: 8, PauseTotalNs: 9, Uptime: 10}, "080110021803200428053006380740084809500a"},
		{"ip response", &xrpc.GetStatsOnlineIpListResponse{Name: "user", Ips: map[string]int64{"203.0.113.1": 123}}, "0a0475736572120f0a0b3230332e302e3131332e31107b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := marshal.Marshal(test.message)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(raw); got != test.want {
				t.Fatalf("wire = %s, want %s", got, test.want)
			}
		})
	}
}

func TestRPCMethodPathGoldens(t *testing.T) {
	methods := map[string]string{
		"handler alter inbound":        handlerAlterInboundMethod,
		"handler get inbound users":    handlerGetInboundUsersMethod,
		"handler get inbound count":    handlerGetInboundUserCountMethod,
		"handler remove outbound":      handlerRemoveOutboundMethod,
		"stats system":                 statsGetSysStatsMethod,
		"stats online":                 statsGetStatsOnlineMethod,
		"stats query":                  statsQueryStatsMethod,
		"stats online IP list":         statsGetStatsOnlineIPListMethod,
		"stats all online users":       statsGetAllOnlineUsersMethod,
		"stats native users extension": getUsersStatsMethod,
	}
	want := map[string]string{
		"handler alter inbound":        "/xray.app.proxyman.command.HandlerService/AlterInbound",
		"handler get inbound users":    "/xray.app.proxyman.command.HandlerService/GetInboundUsers",
		"handler get inbound count":    "/xray.app.proxyman.command.HandlerService/GetInboundUsersCount",
		"handler remove outbound":      "/xray.app.proxyman.command.HandlerService/RemoveOutbound",
		"stats system":                 "/xray.app.stats.command.StatsService/GetSysStats",
		"stats online":                 "/xray.app.stats.command.StatsService/GetStatsOnline",
		"stats query":                  "/xray.app.stats.command.StatsService/QueryStats",
		"stats online IP list":         "/xray.app.stats.command.StatsService/GetStatsOnlineIpList",
		"stats all online users":       "/xray.app.stats.command.StatsService/GetAllOnlineUsers",
		"stats native users extension": "/xray.app.stats.command.StatsService/GetUsersStats",
	}
	for name, method := range methods {
		if method != want[name] {
			t.Errorf("%s method = %q, want %q", name, method, want[name])
		}
	}
}

func TestGRPCResponseLimitFitsLowMemoryTarget(t *testing.T) {
	if maxGRPCMessageBytes != 16<<20 {
		t.Fatalf("gRPC response limit = %d, want 16 MiB", maxGRPCMessageBytes)
	}
}
