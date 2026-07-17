package xtls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls/xrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetInboundUsersMatchesOfficialPublicModel(t *testing.T) {
	t.Parallel()

	conn := &fakeInvokeConn{invoke: func(_ context.Context, method string, _, reply any, _ ...grpc.CallOption) error {
		if method != handlerGetInboundUsersMethod {
			return fmt.Errorf("method = %q", method)
		}
		reply.(*xrpc.GetInboundUserResponse).Users = []*xrpc.User{
			{Email: "level-zero", Level: 0, Account: &xrpc.TypedMessage{Type: "xray.proxy.vless.Account"}},
			{Email: "ss-user", Level: 2, Account: &xrpc.TypedMessage{Type: "xray.proxy.shadowsocks.Account"}},
		}
		return nil
	}}
	users, result := NewHandlerAPI(conn).GetInboundUsers(context.Background(), "in")
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	want := []InboundUser{
		{Username: "level-zero", Level: 0, Protocol: "vless"},
		{Username: "ss-user", Level: 2, Protocol: "shadowsocks"},
	}
	if !slices.Equal(users, want) {
		t.Fatalf("users = %+v, want %+v", users, want)
	}
	raw, err := json.Marshal(users[0])
	if err != nil {
		t.Fatal(err)
	}
	// Official Node model selects exactly username, level and protocol.
	if got, wantJSON := string(raw), `{"username":"level-zero","level":0,"protocol":"vless"}`; got != wantJSON {
		t.Fatalf("JSON = %s, want %s", got, wantJSON)
	}
}

func TestInboundUserProtocolsMatchOfficialSDK(t *testing.T) {
	t.Parallel()

	// Oracle: @remnawave/xtls-sdk 0.16.0 ACCOUNT_TYPES.
	tests := map[string]string{
		"xray.proxy.trojan.Account":           "trojan",
		"xray.proxy.vless.Account":            "vless",
		"xray.proxy.shadowsocks.Account":      "shadowsocks",
		"xray.proxy.shadowsocks_2022.Account": "shadowsocks2022",
		"xray.proxy.socks.Account":            "socks",
		"xray.proxy.http.Account":             "http",
	}
	for accountType, want := range tests {
		got, ok := inboundUserProtocol(accountType)
		if !ok || got != want {
			t.Errorf("inboundUserProtocol(%q) = %q, %v; want %q, true", accountType, got, ok, want)
		}
	}
}

func TestGetInboundUsersRejectsUnknownAccountType(t *testing.T) {
	t.Parallel()

	conn := &fakeInvokeConn{invoke: func(_ context.Context, _ string, _, reply any, _ ...grpc.CallOption) error {
		reply.(*xrpc.GetInboundUserResponse).Users = []*xrpc.User{{
			Email: "unknown", Account: &xrpc.TypedMessage{Type: "example.UnknownAccount"},
		}}
		return nil
	}}
	users, result := NewHandlerAPI(conn).GetInboundUsers(context.Background(), "in")
	if result.OK || users != nil {
		t.Fatalf("users = %+v, result = %+v; want all-or-nothing decode failure", users, result)
	}
}

func TestIsUserNotFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found code", status.Error(codes.NotFound, "user missing"), true},
		{"not found message", status.Error(codes.Unknown, "user not found in inbound"), true},
		{"other", status.Error(codes.Internal, "boom"), false},
		{"plain", errors.New("not exist"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isUserNotFound(tc.err); got != tc.want {
				t.Fatalf("isUserNotFound() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsUserExists(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"already exists", status.Error(codes.AlreadyExists, "user already exists"), true},
		{"duplicate", status.Error(codes.Unknown, "duplicate user email"), true},
		{"other", status.Error(codes.Internal, "boom"), false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isUserExists(tc.err); got != tc.want {
				t.Fatalf("isUserExists() = %v, want %v", got, tc.want)
			}
		})
	}
}
