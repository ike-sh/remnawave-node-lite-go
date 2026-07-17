package xtls

import (
	"context"
	"fmt"
	"strings"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls/xrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	handlerAlterInboundMethod        = "/xray.app.proxyman.command.HandlerService/AlterInbound"
	handlerGetInboundUsersMethod     = "/xray.app.proxyman.command.HandlerService/GetInboundUsers"
	handlerGetInboundUserCountMethod = "/xray.app.proxyman.command.HandlerService/GetInboundUsersCount"
	handlerRemoveOutboundMethod      = "/xray.app.proxyman.command.HandlerService/RemoveOutbound"
)

const (
	addUserOperationType       = "xray.app.proxyman.command.AddUserOperation"
	removeUserOperationType    = "xray.app.proxyman.command.RemoveUserOperation"
	vlessAccountType           = "xray.proxy.vless.Account"
	trojanAccountType          = "xray.proxy.trojan.Account"
	shadowsocksAccountType     = "xray.proxy.shadowsocks.Account"
	shadowsocks2022AccountType = "xray.proxy.shadowsocks_2022.Account"
	hysteriaAccountType        = "xray.proxy.hysteria.account.Account"
	socksAccountType           = "xray.proxy.socks.Account"
	httpAccountType            = "xray.proxy.http.Account"
)

type HandlerResult struct {
	OK         bool
	Message    string
	Generation uint64
}

type InboundUser struct {
	Username string `json:"username"`
	Level    uint32 `json:"level"`
	Protocol string `json:"protocol"`
}

type HandlerAPI struct {
	conn grpc.ClientConnInterface
}

func NewHandlerAPI(conn grpc.ClientConnInterface) *HandlerAPI {
	return &HandlerAPI{conn: conn}
}

func (h *HandlerAPI) AddVlessUser(ctx context.Context, tag, username, uuid, flow string, level uint32) HandlerResult {
	return h.addAccountUser(ctx, tag, username, level, vlessAccountType, &xrpc.VlessAccount{
		Id: uuid, Flow: flow, Encryption: "none",
	})
}

func (h *HandlerAPI) AddTrojanUser(ctx context.Context, tag, username, password string, level uint32) HandlerResult {
	return h.addAccountUser(ctx, tag, username, level, trojanAccountType, &xrpc.TrojanAccount{Password: password})
}

func (h *HandlerAPI) AddShadowsocksUser(ctx context.Context, tag, username, password string, cipherType int, ivCheck bool, level uint32) HandlerResult {
	return h.addAccountUser(ctx, tag, username, level, shadowsocksAccountType, &xrpc.ShadowsocksAccount{
		Password: password, CipherType: int32(cipherType), IvCheck: ivCheck,
	})
}

func (h *HandlerAPI) AddShadowsocks2022User(ctx context.Context, tag, username, key string, level uint32) HandlerResult {
	return h.addAccountUser(ctx, tag, username, level, shadowsocks2022AccountType, &xrpc.Shadowsocks2022Account{Key: key})
}

func (h *HandlerAPI) AddHysteriaUser(ctx context.Context, tag, username, auth string, level uint32) HandlerResult {
	return h.addAccountUser(ctx, tag, username, level, hysteriaAccountType, &xrpc.HysteriaAccount{Auth: auth})
}

func (h *HandlerAPI) RemoveOutbound(ctx context.Context, tag string) error {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	err := h.conn.Invoke(ctx, handlerRemoveOutboundMethod, &xrpc.RemoveOutboundRequest{Tag: tag}, &xrpc.Empty{}, grpc.StaticMethod())
	return err
}

func (h *HandlerAPI) RemoveUser(ctx context.Context, tag, username string) HandlerResult {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	operation, marshalErr := typedMessage(removeUserOperationType, &xrpc.RemoveUserOperation{Email: username})
	if marshalErr != nil {
		return HandlerResult{OK: false, Message: marshalErr.Error()}
	}
	err := h.conn.Invoke(ctx, handlerAlterInboundMethod, &xrpc.AlterInboundRequest{Tag: tag, Operation: operation}, &xrpc.Empty{}, grpc.StaticMethod())
	if err == nil || isUserNotFound(err) {
		return HandlerResult{OK: true}
	}
	return HandlerResult{OK: false, Message: grpcErrorMessage(err)}
}

func (h *HandlerAPI) GetInboundUsers(ctx context.Context, tag string) ([]InboundUser, HandlerResult) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.GetInboundUserResponse{}
	err := h.conn.Invoke(ctx, handlerGetInboundUsersMethod, &xrpc.GetInboundUserRequest{Tag: tag}, resp, grpc.StaticMethod())
	if err != nil {
		return nil, HandlerResult{OK: false, Message: grpcErrorMessage(err)}
	}

	users := make([]InboundUser, 0, len(resp.GetUsers()))
	for _, user := range resp.GetUsers() {
		if user == nil {
			return nil, HandlerResult{OK: false, Message: "invalid inbound user: missing user"}
		}
		account := user.GetAccount()
		if account == nil {
			return nil, HandlerResult{OK: false, Message: "invalid inbound user: missing account"}
		}
		protocol, ok := inboundUserProtocol(account.GetType())
		if !ok {
			return nil, HandlerResult{
				OK:      false,
				Message: fmt.Sprintf("unsupported inbound user account type %q", account.GetType()),
			}
		}
		users = append(users, InboundUser{
			Username: user.GetEmail(),
			Level:    user.GetLevel(),
			Protocol: protocol,
		})
	}
	return users, HandlerResult{OK: true}
}

func inboundUserProtocol(accountType string) (string, bool) {
	switch accountType {
	case trojanAccountType:
		return "trojan", true
	case vlessAccountType:
		return "vless", true
	case shadowsocksAccountType:
		return "shadowsocks", true
	case shadowsocks2022AccountType:
		return "shadowsocks2022", true
	case socksAccountType:
		return "socks", true
	case httpAccountType:
		return "http", true
	default:
		return "", false
	}
}

func (h *HandlerAPI) GetInboundUsersCount(ctx context.Context, tag string) (int64, HandlerResult) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.GetInboundUsersCountResponse{}
	err := h.conn.Invoke(ctx, handlerGetInboundUserCountMethod, &xrpc.GetInboundUserRequest{Tag: tag}, resp, grpc.StaticMethod())
	if err != nil {
		return 0, HandlerResult{OK: false, Message: grpcErrorMessage(err)}
	}
	return resp.GetCount(), HandlerResult{OK: true}
}

func (h *HandlerAPI) addAccountUser(ctx context.Context, tag, username string, level uint32, accountType string, account proto.Message) HandlerResult {
	typedAccount, err := typedMessage(accountType, account)
	if err != nil {
		return HandlerResult{OK: false, Message: err.Error()}
	}
	return h.addUser(ctx, tag, &xrpc.User{Email: username, Level: level, Account: typedAccount})
}

func (h *HandlerAPI) addUser(ctx context.Context, tag string, user *xrpc.User) HandlerResult {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	operation, marshalErr := typedMessage(addUserOperationType, &xrpc.AddUserOperation{User: user})
	if marshalErr != nil {
		return HandlerResult{OK: false, Message: marshalErr.Error()}
	}
	err := h.conn.Invoke(ctx, handlerAlterInboundMethod, &xrpc.AlterInboundRequest{Tag: tag, Operation: operation}, &xrpc.Empty{}, grpc.StaticMethod())
	if err == nil {
		return HandlerResult{OK: true}
	}
	if isUserExists(err) {
		return HandlerResult{OK: false, Message: grpcErrorMessage(err)}
	}
	return HandlerResult{OK: false, Message: grpcErrorMessage(err)}
}

func typedMessage(typeName string, message proto.Message) (*xrpc.TypedMessage, error) {
	raw, err := proto.Marshal(message)
	if err != nil {
		return nil, err
	}
	return &xrpc.TypedMessage{Type: typeName, Value: raw}, nil
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
