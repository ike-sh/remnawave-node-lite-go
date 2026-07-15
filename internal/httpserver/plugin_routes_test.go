package httpserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
)

type recordingPluginController struct {
	calls      atomic.Int64
	syncPlugin *plugin.SyncPlugin
	blockItems []plugin.BlockIP
}

func (p *recordingPluginController) hit() { p.calls.Add(1) }
func (p *recordingPluginController) ResetPlugins() {
	p.hit()
}
func (p *recordingPluginController) Sync(request *plugin.SyncPlugin) plugin.AcceptedResponse {
	p.hit()
	p.syncPlugin = request
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) CollectReports() plugin.CollectReportsResponse {
	p.hit()
	return plugin.CollectReportsResponse{Reports: []plugin.TorrentReport{}}
}
func (p *recordingPluginController) BlockIPs(items []plugin.BlockIP) plugin.AcceptedResponse {
	p.hit()
	p.blockItems = items
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) UnblockIPs([]string) plugin.AcceptedResponse {
	p.hit()
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) RecreateTables() plugin.AcceptedResponse {
	p.hit()
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) ReportsCount() int { return 0 }

func TestPluginValidationPrecedesServiceCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "sync missing plugin", path: "/node/plugin/sync", body: `{}`},
		{name: "sync bad UUID", path: "/node/plugin/sync", body: `{"plugin":{"config":{},"uuid":"bad","name":"p"}}`},
		{name: "sync config not object", path: "/node/plugin/sync", body: `{"plugin":{"config":[],"uuid":"00000000-0000-4000-8000-000000000001","name":"p"}}`},
		{name: "block invalid IP", path: "/node/plugin/nftables/block-ips", body: `{"ips":[{"ip":"bad","timeout":60}]}`},
		{name: "block missing timeout", path: "/node/plugin/nftables/block-ips", body: `{"ips":[{"ip":"203.0.113.10"}]}`},
		{name: "unblock invalid IP", path: "/node/plugin/nftables/unblock-ips", body: `{"ips":["bad"]}`},
		{name: "trailing JSON", path: "/node/plugin/nftables/unblock-ips", body: `{"ips":[]}{"ips":[]}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			controller := &recordingPluginController{}
			server := &Server{pluginService: controller}
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			rec := httptest.NewRecorder()

			server.handleNodeRoutes(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if controller.calls.Load() != 0 {
				t.Fatalf("plugin service calls = %d, want 0", controller.calls.Load())
			}
		})
	}
}

func TestPluginRoutesProduceOfficialResponseShapes(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/node/plugin/sync",
		"/node/plugin/torrent-blocker/collect",
		"/node/plugin/nftables/block-ips",
		"/node/plugin/nftables/unblock-ips",
		"/node/plugin/nftables/recreate-tables",
	}
	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			route, _ := contractspec.FindRouteByPath(path)
			controller := &recordingPluginController{}
			server := &Server{pluginService: controller}
			req := httptest.NewRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest))
			rec := httptest.NewRecorder()

			server.handleNodeRoutes(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if err := contractspec.ValidateResponse(path, rec.Body.Bytes()); err != nil {
				t.Fatalf("response violates official schema: %v\n%s", err, rec.Body.Bytes())
			}
		})
	}
}

func TestPluginTransportPreservesConfigJSONAndFractionalTimeout(t *testing.T) {
	t.Parallel()

	controller := &recordingPluginController{}
	server := &Server{pluginService: controller}
	syncBody := `{"plugin":{"config":{"z":1,"a":2},"uuid":"00000000-0000-4000-8000-000000000001","name":"p"}}`
	syncRequest := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", strings.NewReader(syncBody))
	syncRecorder := httptest.NewRecorder()
	server.handleNodeRoutes(syncRecorder, syncRequest)

	if controller.syncPlugin == nil {
		t.Fatal("sync plugin was not dispatched")
	}
	if string(controller.syncPlugin.Config) != `{"z":1,"a":2}` {
		t.Fatalf("config = %s, want original key order", controller.syncPlugin.Config)
	}

	blockBody := `{"ips":[{"ip":"2001:db8::1","timeout":1.5}]}`
	blockRequest := httptest.NewRequest(http.MethodPost, "/node/plugin/nftables/block-ips", strings.NewReader(blockBody))
	blockRecorder := httptest.NewRecorder()
	server.handleNodeRoutes(blockRecorder, blockRequest)
	if len(controller.blockItems) != 1 || controller.blockItems[0].Timeout != 1.5 {
		t.Fatalf("block items = %+v, want fractional timeout", controller.blockItems)
	}
}
