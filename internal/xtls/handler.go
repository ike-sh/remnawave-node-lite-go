package xtls

import (
	"context"
	"strings"

	proxcommand "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	hysteria "github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type HandlerResult struct {
	OK      bool
	Message string
}

type InboundUser struct {
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Level    uint32 `json:"level,omitempty"`
}

type HandlerAPI struct {
	client proxcommand.HandlerServiceClient
}

func NewHandlerAPI(conn *grpc.ClientConn) *HandlerAPI {
	return &HandlerAPI{client: proxcommand.NewHandlerServiceClient(conn)}
}

func (h *HandlerAPI) AddVlessUser(ctx context.Context, tag, username, uuid, flow string, level uint32) HandlerResult {
	user := &protocol.User{
		Email: username,
		Level: level,
		Account: serial.ToTypedMessage(&vless.Account{
			Id:   uuid,
			Flow: flow,
		}),
	}
	return h.addUser(ctx, tag, user)
}

func (h *HandlerAPI) AddTrojanUser(ctx context.Context, tag, username, password string, level uint32) HandlerResult {
	user := &protocol.User{
		Email: username,
		Level: level,
		Account: serial.ToTypedMessage(&trojan.Account{
			Password: password,
		}),
	}
	return h.addUser(ctx, tag, user)
}

func (h *HandlerAPI) AddShadowsocksUser(ctx context.Context, tag, username, password string, cipherType int, ivCheck bool, level uint32) HandlerResult {
	user := &protocol.User{
		Email: username,
		Level: level,
		Account: serial.ToTypedMessage(&shadowsocks.Account{
			Password:   password,
			CipherType: shadowsocks.CipherType(cipherType),
			IvCheck:    ivCheck,
		}),
	}
	return h.addUser(ctx, tag, user)
}

func (h *HandlerAPI) AddShadowsocks2022User(ctx context.Context, tag, username, key string, level uint32) HandlerResult {
	user := &protocol.User{
		Email: username,
		Level: level,
		Account: serial.ToTypedMessage(&ss2022.Account{
			Key: key,
		}),
	}
	return h.addUser(ctx, tag, user)
}

func (h *HandlerAPI) AddHysteriaUser(ctx context.Context, tag, username, auth string, level uint32) HandlerResult {
	user := &protocol.User{
		Email: username,
		Level: level,
		Account: serial.ToTypedMessage(&hysteria.Account{
			Auth: auth,
		}),
	}
	return h.addUser(ctx, tag, user)
}

func (h *HandlerAPI) RemoveOutbound(ctx context.Context, tag string) error {
	_, err := h.client.RemoveOutbound(ctx, &proxcommand.RemoveOutboundRequest{Tag: tag})
	return err
}

func (h *HandlerAPI) RemoveUser(ctx context.Context, tag, username string) HandlerResult {
	_, err := h.client.AlterInbound(ctx, &proxcommand.AlterInboundRequest{
		Tag: tag,
		Operation: serial.ToTypedMessage(&proxcommand.RemoveUserOperation{
			Email: username,
		}),
	})
	if err == nil || isUserNotFound(err) {
		return HandlerResult{OK: true}
	}
	return HandlerResult{OK: false, Message: grpcErrorMessage(err)}
}

func (h *HandlerAPI) GetInboundUsers(ctx context.Context, tag string) ([]InboundUser, HandlerResult) {
	resp, err := h.client.GetInboundUsers(ctx, &proxcommand.GetInboundUserRequest{Tag: tag})
	if err != nil {
		return nil, HandlerResult{OK: false, Message: grpcErrorMessage(err)}
	}

	users := make([]InboundUser, 0, len(resp.GetUsers()))
	for _, user := range resp.GetUsers() {
		if user == nil {
			continue
		}
		users = append(users, InboundUser{
			Username: user.GetEmail(),
			Email:    user.GetEmail(),
			Level:    user.GetLevel(),
		})
	}
	return users, HandlerResult{OK: true}
}

func (h *HandlerAPI) GetInboundUsersCount(ctx context.Context, tag string) (int64, HandlerResult) {
	resp, err := h.client.GetInboundUsersCount(ctx, &proxcommand.GetInboundUserRequest{Tag: tag})
	if err != nil {
		return 0, HandlerResult{OK: false, Message: grpcErrorMessage(err)}
	}
	return resp.GetCount(), HandlerResult{OK: true}
}

func (h *HandlerAPI) addUser(ctx context.Context, tag string, user *protocol.User) HandlerResult {
	_, err := h.client.AlterInbound(ctx, &proxcommand.AlterInboundRequest{
		Tag: tag,
		Operation: serial.ToTypedMessage(&proxcommand.AddUserOperation{
			User: user,
		}),
	})
	if err == nil {
		return HandlerResult{OK: true}
	}
	if isUserExists(err) {
		return HandlerResult{OK: false, Message: grpcErrorMessage(err)}
	}
	return HandlerResult{OK: false, Message: grpcErrorMessage(err)}
}

func isUserNotFound(err error) bool {
	if st, ok := status.FromError(err); ok {
		if st.Code() == codes.NotFound {
			return true
		}
		msg := strings.ToLower(st.Message())
		return strings.Contains(msg, "not found") ||
			strings.Contains(msg, "not exist") ||
			strings.Contains(msg, "no such user")
	}
	return false
}

func isUserExists(err error) bool {
	if st, ok := status.FromError(err); ok {
		msg := strings.ToLower(st.Message())
		return strings.Contains(msg, "already exists") ||
			strings.Contains(msg, "already exist") ||
			strings.Contains(msg, "duplicate")
	}
	return false
}

func grpcErrorMessage(err error) string {
	if st, ok := status.FromError(err); ok {
		return st.Message()
	}
	return err.Error()
}
