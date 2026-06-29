package nodehandler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/nodehandler"
	"remnawave-node-lite-go/internal/xtls"
)

type stubProvider struct {
	inboundTags []string
}

func (s *stubProvider) AddInboundTag(tag string) {
	s.inboundTags = append(s.inboundTags, tag)
}
func (s *stubProvider) InboundTags() []string                    { return s.inboundTags }
func (s *stubProvider) AddUserToInboundHash(string, string)      {}
func (s *stubProvider) RemoveUserFromInboundHash(string, string) {}
func (s *stubProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	return nil, nil
}
func (s *stubProvider) HandlerRemoveUser(context.Context, string, string) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (s *stubProvider) HandlerAddVlessUser(context.Context, string, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: false, Message: "boom"}
}
func (s *stubProvider) HandlerAddTrojanUser(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (s *stubProvider) HandlerAddShadowsocksUser(context.Context, string, string, string, int, bool, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (s *stubProvider) HandlerAddShadowsocks2022User(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (s *stubProvider) HandlerAddHysteriaUser(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (s *stubProvider) HandlerGetInboundUsers(context.Context, string) ([]xtls.InboundUser, xtls.HandlerResult) {
	return nil, xtls.HandlerResult{OK: true}
}
func (s *stubProvider) HandlerGetInboundUsersCount(context.Context, string) (int64, xtls.HandlerResult) {
	return 0, xtls.HandlerResult{OK: true}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestHandleAddUsersAlwaysSuccess(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(&stubProvider{}, connections.NewDropper(nil))
	body := `{
		"affectedInboundTags":["in-1"],
		"users":[{
			"inboundData":[{"type":"vless","tag":"in-1","flow":""}],
			"userData":{"userId":"u1","hashUuid":"h1","vlessUuid":"uuid-1","trojanPassword":"","ssPassword":""}
		}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/node/handler/add-users", strings.NewReader(body))
	rec := httptest.NewRecorder()

	service.HandleAddUsers(rec, req, writeJSON)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Response struct {
			Success bool    `json:"success"`
			Error   *string `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Response.Success {
		t.Fatalf("success = false, error = %v", resp.Response.Error)
	}
}

type failingInboundProvider struct{}

func (failingInboundProvider) AddInboundTag(string)                     {}
func (failingInboundProvider) InboundTags() []string                    { return nil }
func (failingInboundProvider) AddUserToInboundHash(string, string)      {}
func (failingInboundProvider) RemoveUserFromInboundHash(string, string) {}
func (failingInboundProvider) GetUserIPList(context.Context, string, bool) ([]xtls.IPEntry, error) {
	return nil, nil
}
func (failingInboundProvider) HandlerRemoveUser(context.Context, string, string) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerAddVlessUser(context.Context, string, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerAddTrojanUser(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerAddShadowsocksUser(context.Context, string, string, string, int, bool, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerAddShadowsocks2022User(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerAddHysteriaUser(context.Context, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerGetInboundUsers(context.Context, string) ([]xtls.InboundUser, xtls.HandlerResult) {
	return nil, xtls.HandlerResult{OK: true}
}
func (failingInboundProvider) HandlerGetInboundUsersCount(context.Context, string) (int64, xtls.HandlerResult) {
	return 0, xtls.HandlerResult{OK: false, Message: "xray is not online"}
}

func TestHandleGetInboundUsersCountGRPCFailure(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(failingInboundProvider{}, connections.NewDropper(nil))
	body := `{"tag":"in-1"}`
	req := httptest.NewRequest(http.MethodPost, "/node/handler/get-inbound-users-count", strings.NewReader(body))
	rec := httptest.NewRecorder()

	service.HandleGetInboundUsersCount(rec, req, writeJSON)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["errorCode"] != "A014" {
		t.Fatalf("errorCode = %v, want A014", resp["errorCode"])
	}
}

func TestHandleAddUserReportsFailureWhenAllFail(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(&stubProvider{}, connections.NewDropper(nil))
	body := `{
		"data":[{"type":"vless","tag":"in-1","username":"u1","uuid":"x","flow":""}],
		"hashData":{"vlessUuid":"uuid-1"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/node/handler/add-user", strings.NewReader(body))
	rec := httptest.NewRecorder()

	service.HandleAddUser(rec, req, writeJSON)

	var resp struct {
		Response struct {
			Success bool `json:"success"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Response.Success {
		t.Fatal("expected add-user failure when all operations fail")
	}
}
