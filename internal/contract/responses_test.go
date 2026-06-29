package contract_test

import (
	"context"
	"encoding/json"
	"net/http"

	"remnawave-node-lite-go/internal/xtls"
)

type stubHandlerProvider struct{}

func (stubHandlerProvider) AddInboundTag(string)                     {}
func (stubHandlerProvider) InboundTags() []string                    { return nil }
func (stubHandlerProvider) AddUserToInboundHash(string, string)      {}
func (stubHandlerProvider) RemoveUserFromInboundHash(string, string) {}
func (stubHandlerProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	return nil, nil
}
func (stubHandlerProvider) HandlerRemoveUser(context.Context, string, string) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (stubHandlerProvider) HandlerAddVlessUser(context.Context, string, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: false, Message: "offline"}
}
func (stubHandlerProvider) HandlerAddTrojanUser(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: false}
}
func (stubHandlerProvider) HandlerAddShadowsocksUser(context.Context, string, string, string, int, bool, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: false}
}
func (stubHandlerProvider) HandlerAddShadowsocks2022User(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: false}
}
func (stubHandlerProvider) HandlerAddHysteriaUser(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: false}
}
func (stubHandlerProvider) HandlerGetInboundUsers(context.Context, string) ([]xtls.InboundUser, xtls.HandlerResult) {
	return nil, xtls.HandlerResult{OK: true}
}
func (stubHandlerProvider) HandlerGetInboundUsersCount(context.Context, string) (int64, xtls.HandlerResult) {
	return 0, xtls.HandlerResult{OK: true}
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
