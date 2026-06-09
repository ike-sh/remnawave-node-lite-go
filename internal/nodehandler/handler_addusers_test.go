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

type hashTrackingProvider struct {
	stubProvider
	hashAdds []string
}

func (p *hashTrackingProvider) AddUserToInboundHash(tag, uuid string) {
	p.hashAdds = append(p.hashAdds, tag+":"+uuid)
}

func TestHandleAddUsersSkipsHashOnHandlerFailure(t *testing.T) {
	t.Parallel()

	provider := &hashTrackingProvider{stubProvider: stubProvider{inboundTags: []string{"in-1"}}}
	service := nodehandler.NewService(provider, connections.NewDropper(nil))
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

func (p *successVlessProvider) AddUserToInboundHash(tag, uuid string) {
	p.hashAdds = append(p.hashAdds, tag+":"+uuid)
}

func TestHandleAddUsersAddsHashOnHandlerSuccess(t *testing.T) {
	t.Parallel()

	provider := &successVlessProvider{stubProvider: stubProvider{inboundTags: []string{"in-1"}}}
	service := nodehandler.NewService(provider, connections.NewDropper(nil))
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

	if len(provider.hashAdds) != 1 || provider.hashAdds[0] != "in-1:uuid-1" {
		t.Fatalf("unexpected hash adds: %#v", provider.hashAdds)
	}

	var resp struct {
		Response struct {
			Success bool `json:"success"`
		} `json:"response"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Response.Success {
		t.Fatal("expected success=true matching upstream addUsers contract")
	}
}
