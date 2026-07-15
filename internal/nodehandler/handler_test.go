package nodehandler_test

import (
	"context"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodehandler"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

type stubProvider struct {
	inboundTags []string
}

func (s *stubProvider) AddInboundTag(tag string) {
	s.inboundTags = append(s.inboundTags, tag)
}
func (s *stubProvider) InboundTags() []string { return s.inboundTags }
func (s *stubProvider) CommitUserAdded(xtls.HandlerResult, string, string) bool {
	return true
}
func (s *stubProvider) CommitUserRemoved(xtls.HandlerResult, string, string) bool {
	return true
}
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

func TestAddUsersReportsHandlerFailure(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(&stubProvider{}, connections.NewDropper(nil))
	response, err := service.AddUsers(context.Background(), nodehandler.AddUsersRequest{
		AffectedInboundTags: []string{"in-1"},
		Users: []nodehandler.BatchUser{{
			InboundData: []nodehandler.BatchInbound{{Type: "vless", Tag: "in-1", Flow: ""}},
			UserData: nodehandler.BatchUserData{
				UserID: "u1", HashUUID: "h1", VlessUUID: "uuid-1",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error == nil || *response.Error != "boom" {
		t.Fatalf("response = %+v, want handler failure", response)
	}
}

type failingInboundProvider struct {
	stubProvider
}

func (failingInboundProvider) HandlerGetInboundUsersCount(context.Context, string) (int64, xtls.HandlerResult) {
	return 0, xtls.HandlerResult{OK: false, Message: "xray is not online"}
}

func TestGetInboundUsersCountGRPCFailure(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(&failingInboundProvider{}, connections.NewDropper(nil))
	_, err := service.GetInboundUsersCount(context.Background(), "in-1")
	serviceError, ok := nodeapi.AsServiceError(err)
	if !ok || serviceError.Code != "A014" || serviceError.Message != "Failed to get inbound users" {
		t.Fatalf("error = %+v, want A014", err)
	}
}

func TestAddUserReportsFailureWhenAllFail(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(&stubProvider{}, connections.NewDropper(nil))
	response, err := service.AddUser(context.Background(), nodehandler.AddUserRequest{
		Data: []nodehandler.AddUserItem{{
			Type: "vless", Tag: "in-1", Username: "u1", UUID: "x", Flow: "",
		}},
		HashData: nodehandler.AddUserHashData{VlessUUID: "uuid-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error == nil || *response.Error != "boom" {
		t.Fatalf("response = %+v, want handler failure", response)
	}
}

func TestAddUserEmptyDataFailsWithoutPanic(t *testing.T) {
	t.Parallel()

	service := nodehandler.NewService(&stubProvider{inboundTags: []string{"in-1"}}, connections.NewDropper(nil))
	response, err := service.AddUser(context.Background(), nodehandler.AddUserRequest{
		HashData: nodehandler.AddUserHashData{VlessUUID: "uuid-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Success || response.Error != nil {
		t.Fatalf("response = %+v, want safe failure", response)
	}
}

type shadowsocksTrackingProvider struct {
	stubProvider
	ivChecks []bool
}

func (p *shadowsocksTrackingProvider) HandlerAddShadowsocksUser(_ context.Context, _, _, _ string, _ int, ivCheck bool, _ uint32) xtls.HandlerResult {
	p.ivChecks = append(p.ivChecks, ivCheck)
	return xtls.HandlerResult{OK: true}
}

func TestAddUserMatchesOfficialShadowsocksIVCheckBehavior(t *testing.T) {
	t.Parallel()

	provider := &shadowsocksTrackingProvider{}
	service := nodehandler.NewService(provider, connections.NewDropper(nil))
	_, err := service.AddUser(context.Background(), nodehandler.AddUserRequest{
		Data: []nodehandler.AddUserItem{{
			Type: "shadowsocks", Tag: "in-1", Username: "u1", Password: "secret", CipherType: 5, IVCheck: true,
		}},
		HashData: nodehandler.AddUserHashData{VlessUUID: "uuid-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.ivChecks) != 1 || provider.ivChecks[0] {
		t.Fatalf("ivCheck calls = %v, want [false] matching official 2.8.0", provider.ivChecks)
	}
}
