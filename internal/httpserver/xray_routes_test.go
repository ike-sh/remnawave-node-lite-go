package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/system"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xray"
)

type recordingXrayController struct {
	startCalls atomic.Int64
	request    xray.StartRequest
}

func (x *recordingXrayController) Start(_ context.Context, request xray.StartRequest) xray.StartResponse {
	x.startCalls.Add(1)
	x.request = request
	return xray.StartResponse{
		IsStarted:       true,
		Version:         nil,
		Error:           nil,
		NodeInformation: xray.NodeInformation{Version: nil},
		System:          system.GetSnapshot(),
	}
}

func (x *recordingXrayController) Stop() xray.StopResponse {
	return xray.StopResponse{IsStopped: true}
}

func (x *recordingXrayController) Health() xray.HealthResponse {
	return xray.HealthResponse{}
}

func TestXrayStartValidationPrecedesManagerCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "missing all", body: `{}`},
		{name: "missing hashes", body: `{"internals":{},"xrayConfig":{}}`},
		{name: "null force restart", body: `{"internals":{"forceRestart":null,"hashes":{"emptyConfig":"h","inbounds":[]}},"xrayConfig":{}}`},
		{name: "inbound missing hash", body: `{"internals":{"hashes":{"emptyConfig":"h","inbounds":[{"usersCount":1,"tag":"in"}]}},"xrayConfig":{}}`},
		{name: "config not object", body: `{"internals":{"hashes":{"emptyConfig":"h","inbounds":[]}},"xrayConfig":[]}`},
		{name: "trailing JSON", body: `{"internals":{"hashes":{"emptyConfig":"h","inbounds":[]}},"xrayConfig":{}}{}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manager := &recordingXrayController{}
			server := &Server{manager: manager}
			req := httptest.NewRequest(http.MethodPost, "/node/xray/start", strings.NewReader(test.body))
			rec := httptest.NewRecorder()

			server.handleNodeRoutes(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if manager.startCalls.Load() != 0 {
				t.Fatalf("manager start calls = %d, want 0", manager.startCalls.Load())
			}
		})
	}
}

func TestXrayStartRouteProducesOfficialResponseShape(t *testing.T) {
	t.Parallel()

	route, _ := contractspec.FindRouteByPath("/node/xray/start")
	manager := &recordingXrayController{}
	server := &Server{manager: manager}
	req := httptest.NewRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest))
	rec := httptest.NewRecorder()

	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := contractspec.ValidateResponse(route.Path, rec.Body.Bytes()); err != nil {
		t.Fatalf("response violates official schema: %v\n%s", err, rec.Body.Bytes())
	}
	if manager.startCalls.Load() != 1 {
		t.Fatalf("manager start calls = %d, want 1", manager.startCalls.Load())
	}
}

func TestXrayStartTransportAppliesDefaultsWithoutCopyingSemantics(t *testing.T) {
	t.Parallel()

	body := `{
		"internals":{"hashes":{"emptyConfig":"h","inbounds":[{"usersCount":1.5,"hash":"ih","tag":"in"}]}},
		"xrayConfig":{"marker":{"value":42}}
	}`
	manager := &recordingXrayController{}
	server := &Server{manager: manager}
	req := httptest.NewRequest(http.MethodPost, "/node/xray/start", strings.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if manager.request.Internals.ForceRestart {
		t.Fatal("missing forceRestart did not default to false")
	}
	if len(manager.request.Internals.Hashes.Inbounds) != 1 || manager.request.Internals.Hashes.Inbounds[0].UsersCount != 1.5 {
		t.Fatalf("inbound hashes = %+v", manager.request.Internals.Hashes.Inbounds)
	}
	marker, ok := manager.request.XrayConfig["marker"].(map[string]any)
	if !ok || marker["value"] != float64(42) {
		t.Fatalf("xrayConfig = %#v", manager.request.XrayConfig)
	}
}
