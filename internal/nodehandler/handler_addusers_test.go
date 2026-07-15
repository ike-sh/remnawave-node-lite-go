package nodehandler_test

import (
	"context"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodehandler"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

type hashTrackingProvider struct {
	stubProvider
	hashAdds []string
}

func (p *hashTrackingProvider) CommitUserAdded(_ xtls.HandlerResult, tag, uuid string) bool {
	p.hashAdds = append(p.hashAdds, tag+":"+uuid)
	return true
}

func TestAddUsersSkipsHashOnHandlerFailure(t *testing.T) {
	t.Parallel()

	provider := &hashTrackingProvider{stubProvider: stubProvider{inboundTags: []string{"in-1"}}}
	service := nodehandler.NewService(provider, connections.NewDropper(nil))
	_, err := service.AddUsers(context.Background(), batchVlessRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.hashAdds) != 0 {
		t.Fatalf("expected no hash updates on handler failure, got %#v", provider.hashAdds)
	}
}

type successVlessProvider struct {
	stubProvider
	hashAdds []string
}

func (p *successVlessProvider) HandlerAddVlessUser(context.Context, string, string, string, string, uint32) xtls.HandlerResult {
	return xtls.HandlerResult{OK: true}
}

func (p *successVlessProvider) CommitUserAdded(_ xtls.HandlerResult, tag, uuid string) bool {
	p.hashAdds = append(p.hashAdds, tag+":"+uuid)
	return true
}

func TestAddUsersAddsHashOnHandlerSuccess(t *testing.T) {
	t.Parallel()

	provider := &successVlessProvider{stubProvider: stubProvider{inboundTags: []string{"in-1"}}}
	service := nodehandler.NewService(provider, connections.NewDropper(nil))
	response, err := service.AddUsers(context.Background(), batchVlessRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.hashAdds) != 1 || provider.hashAdds[0] != "in-1:uuid-1" {
		t.Fatalf("unexpected hash adds: %#v", provider.hashAdds)
	}
	if !response.Success {
		t.Fatal("expected success=true matching upstream addUsers contract")
	}
}

func batchVlessRequest() nodehandler.AddUsersRequest {
	return nodehandler.AddUsersRequest{
		AffectedInboundTags: []string{"in-1"},
		Users: []nodehandler.BatchUser{{
			InboundData: []nodehandler.BatchInbound{{Type: "vless", Tag: "in-1", Flow: ""}},
			UserData: nodehandler.BatchUserData{
				UserID: "u1", HashUUID: "h1", VlessUUID: "uuid-1",
			},
		}},
	}
}
