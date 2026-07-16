package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
)

type recordingPluginController struct {
	calls         atomic.Int64
	recreateCalls atomic.Int64
	syncPlugin    *plugin.SyncPlugin
	blockItems    []plugin.BlockIP
	events        *[]string
	resetOnce     sync.Once
	resetStart    chan struct{}
	resetWait     <-chan struct{}
	syncOnce      sync.Once
	syncStart     chan struct{}
	syncWait      <-chan struct{}
	recreateOnce  sync.Once
	recreateStart chan struct{}
	recreateWait  <-chan struct{}
}

func (p *recordingPluginController) hit() { p.calls.Add(1) }
func (p *recordingPluginController) ResetPluginsContext(ctx context.Context) error {
	p.hit()
	if p.events != nil {
		*p.events = append(*p.events, "reset-plugins")
	}
	if p.resetStart != nil {
		p.resetOnce.Do(func() { close(p.resetStart) })
	}
	if p.resetWait != nil {
		select {
		case <-p.resetWait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (p *recordingPluginController) SyncContext(ctx context.Context, request *plugin.SyncPlugin) plugin.AcceptedResponse {
	p.hit()
	if p.events != nil {
		*p.events = append(*p.events, "sync-plugin")
	}
	p.syncPlugin = request
	if p.syncStart != nil {
		p.syncOnce.Do(func() { close(p.syncStart) })
	}
	if p.syncWait != nil {
		select {
		case <-p.syncWait:
		case <-ctx.Done():
			return plugin.AcceptedResponse{Accepted: false}
		}
	}
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) CollectReports() plugin.CollectReportsResponse {
	p.hit()
	return plugin.CollectReportsResponse{Reports: []plugin.TorrentReport{}}
}
func (p *recordingPluginController) BlockIPsContext(_ context.Context, items []plugin.BlockIP) plugin.AcceptedResponse {
	p.hit()
	p.blockItems = items
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) UnblockIPsContext(context.Context, []string) plugin.AcceptedResponse {
	p.hit()
	return plugin.AcceptedResponse{Accepted: true}
}
func (p *recordingPluginController) RecreateTablesContext(ctx context.Context) plugin.AcceptedResponse {
	p.hit()
	p.recreateCalls.Add(1)
	if p.events != nil {
		*p.events = append(*p.events, "recreate-tables")
	}
	if p.recreateStart != nil {
		p.recreateOnce.Do(func() { close(p.recreateStart) })
	}
	if p.recreateWait != nil {
		select {
		case <-p.recreateWait:
		case <-ctx.Done():
			return plugin.AcceptedResponse{Accepted: false}
		}
	}
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

func TestPluginLifecycleOperationsSerializeWithXrayLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		pluginPath      string
		xrayPath        string
		configurePlugin func(*recordingPluginController, chan struct{}, <-chan struct{})
		wantEvents      string
	}{
		{
			name:       "sync before start",
			pluginPath: "/node/plugin/sync",
			xrayPath:   "/node/xray/start",
			configurePlugin: func(controller *recordingPluginController, started chan struct{}, release <-chan struct{}) {
				controller.syncStart = started
				controller.syncWait = release
			},
			wantEvents: "sync-plugin,start-xray",
		},
		{
			name:       "recreate before start",
			pluginPath: "/node/plugin/nftables/recreate-tables",
			xrayPath:   "/node/xray/start",
			configurePlugin: func(controller *recordingPluginController, started chan struct{}, release <-chan struct{}) {
				controller.recreateStart = started
				controller.recreateWait = release
			},
			wantEvents: "recreate-tables,start-xray",
		},
		{
			name:       "recreate before stop",
			pluginPath: "/node/plugin/nftables/recreate-tables",
			xrayPath:   "/node/xray/stop",
			configurePlugin: func(controller *recordingPluginController, started chan struct{}, release <-chan struct{}) {
				controller.recreateStart = started
				controller.recreateWait = release
			},
			wantEvents: "recreate-tables,stop-xray,reset-plugins",
		},
		{
			name:       "sync before stop",
			pluginPath: "/node/plugin/sync",
			xrayPath:   "/node/xray/stop",
			configurePlugin: func(controller *recordingPluginController, started chan struct{}, release <-chan struct{}) {
				controller.syncStart = started
				controller.syncWait = release
			},
			wantEvents: "sync-plugin,stop-xray,reset-plugins",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			operationStarted := make(chan struct{})
			releaseOperation := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(releaseOperation) }) })
			plugins := &recordingPluginController{events: &events}
			test.configurePlugin(plugins, operationStarted, releaseOperation)
			manager := &recordingXrayController{events: &events}
			server := &Server{manager: manager, pluginService: plugins}

			pluginRoute, _ := contractspec.FindRouteByPath(test.pluginPath)
			pluginResult := serveNodeRouteAsync(server, httptest.NewRequest(
				pluginRoute.Method,
				pluginRoute.Path,
				bytes.NewReader(pluginRoute.ValidRequest),
			))
			awaitTestSignal(t, operationStarted, test.pluginPath)
			assertLifecycleGateHeld(t, server, test.pluginPath)

			waitCtx, cancelWait := context.WithCancel(context.Background())
			defer cancelWait()
			observed := make(chan struct{})
			xrayRoute, _ := contractspec.FindRouteByPath(test.xrayPath)
			xrayRequest := httptest.NewRequest(
				xrayRoute.Method,
				xrayRoute.Path,
				bytes.NewReader(xrayRoute.ValidRequest),
			).WithContext(&observedDoneContext{Context: waitCtx, observed: observed})
			xrayResult := serveNodeRouteAsync(server, xrayRequest)
			awaitTestSignal(t, observed, test.xrayPath+" lifecycle wait")
			assertLifecycleGateHeld(t, server, test.pluginPath)
			if manager.startCalls.Load() != 0 || manager.stopCalls.Load() != 0 {
				t.Fatalf("xray controller ran before %s completed: start=%d stop=%d", test.pluginPath, manager.startCalls.Load(), manager.stopCalls.Load())
			}

			releaseOnce.Do(func() { close(releaseOperation) })
			for name, result := range map[string]<-chan asyncRouteResult{
				"plugin operation": pluginResult,
				"xray operation":   xrayResult,
			} {
				value := awaitRouteResult(t, result, name)
				if value.panicValue != nil || value.response.Code != http.StatusOK {
					t.Fatalf("%s result: panic=%v status=%d body=%s", name, value.panicValue, value.response.Code, value.response.Body.String())
				}
			}
			if test.xrayPath == "/node/xray/start" && manager.startCalls.Load() != 1 {
				t.Fatalf("xray start calls = %d, want 1", manager.startCalls.Load())
			}
			if test.xrayPath == "/node/xray/stop" && manager.stopCalls.Load() != 1 {
				t.Fatalf("xray stop calls = %d, want 1", manager.stopCalls.Load())
			}
			if got := strings.Join(events, ","); got != test.wantEvents {
				t.Fatalf("lifecycle events = %q, want %q", got, test.wantEvents)
			}
		})
	}
}

func TestLifecycleGateCancellationDoesNotReachWaitingController(t *testing.T) {
	t.Parallel()

	t.Run("xray waiter behind plugin sync", func(t *testing.T) {
		t.Parallel()
		events := []string{}
		syncStarted := make(chan struct{})
		releaseSync := make(chan struct{})
		var releaseOnce sync.Once
		t.Cleanup(func() { releaseOnce.Do(func() { close(releaseSync) }) })
		plugins := &recordingPluginController{
			events:    &events,
			syncStart: syncStarted,
			syncWait:  releaseSync,
		}
		manager := &recordingXrayController{events: &events}
		server := &Server{manager: manager, pluginService: plugins}

		syncRoute, _ := contractspec.FindRouteByPath("/node/plugin/sync")
		syncResult := serveNodeRouteAsync(server, httptest.NewRequest(
			syncRoute.Method,
			syncRoute.Path,
			bytes.NewReader(syncRoute.ValidRequest),
		))
		awaitTestSignal(t, syncStarted, "plugin sync")
		assertLifecycleGateHeld(t, server, "plugin sync")

		waitCtx, cancelWait := context.WithCancel(context.Background())
		observed := make(chan struct{})
		stopRoute, _ := contractspec.FindRouteByPath("/node/xray/stop")
		stopRequest := httptest.NewRequest(stopRoute.Method, stopRoute.Path, nil).WithContext(
			&observedDoneContext{Context: waitCtx, observed: observed},
		)
		stopResult := serveNodeRouteAsync(server, stopRequest)
		awaitTestSignal(t, observed, "xray stop lifecycle wait")
		assertLifecycleGateHeld(t, server, "plugin sync")
		cancelWait()
		value := awaitRouteResult(t, stopResult, "canceled xray stop")
		if value.panicValue != http.ErrAbortHandler {
			t.Fatalf("canceled xray stop panic = %#v, want http.ErrAbortHandler", value.panicValue)
		}
		if manager.stopCalls.Load() != 0 {
			t.Fatalf("canceled xray stop reached manager %d times", manager.stopCalls.Load())
		}

		releaseOnce.Do(func() { close(releaseSync) })
		if value := awaitRouteResult(t, syncResult, "plugin sync"); value.panicValue != nil || value.response.Code != http.StatusOK {
			t.Fatalf("plugin sync result: panic=%v status=%d", value.panicValue, value.response.Code)
		}
		if got := strings.Join(events, ","); got != "sync-plugin" {
			t.Fatalf("lifecycle events = %q, want sync-plugin", got)
		}
	})

	t.Run("plugin waiter behind xray stop", func(t *testing.T) {
		t.Parallel()
		events := []string{}
		resetStarted := make(chan struct{})
		releaseReset := make(chan struct{})
		var releaseOnce sync.Once
		t.Cleanup(func() { releaseOnce.Do(func() { close(releaseReset) }) })
		plugins := &recordingPluginController{
			events:     &events,
			resetStart: resetStarted,
			resetWait:  releaseReset,
		}
		manager := &recordingXrayController{events: &events}
		server := &Server{manager: manager, pluginService: plugins}

		stopRoute, _ := contractspec.FindRouteByPath("/node/xray/stop")
		stopResult := serveNodeRouteAsync(server, httptest.NewRequest(stopRoute.Method, stopRoute.Path, nil))
		awaitTestSignal(t, resetStarted, "plugin reset")
		assertLifecycleGateHeld(t, server, "xray stop")

		waitCtx, cancelWait := context.WithCancel(context.Background())
		observed := make(chan struct{})
		recreateRoute, _ := contractspec.FindRouteByPath("/node/plugin/nftables/recreate-tables")
		recreateRequest := httptest.NewRequest(recreateRoute.Method, recreateRoute.Path, nil).WithContext(
			&observedDoneContext{Context: waitCtx, observed: observed},
		)
		recreateResult := serveNodeRouteAsync(server, recreateRequest)
		awaitTestSignal(t, observed, "plugin recreate lifecycle wait")
		assertLifecycleGateHeld(t, server, "xray stop")
		cancelWait()
		value := awaitRouteResult(t, recreateResult, "canceled plugin recreate")
		if value.panicValue != http.ErrAbortHandler {
			t.Fatalf("canceled plugin recreate panic = %#v, want http.ErrAbortHandler", value.panicValue)
		}
		if plugins.recreateCalls.Load() != 0 {
			t.Fatalf("canceled recreate reached plugin controller %d times", plugins.recreateCalls.Load())
		}

		releaseOnce.Do(func() { close(releaseReset) })
		if value := awaitRouteResult(t, stopResult, "xray stop"); value.panicValue != nil || value.response.Code != http.StatusOK {
			t.Fatalf("xray stop result: panic=%v status=%d", value.panicValue, value.response.Code)
		}
		if got := strings.Join(events, ","); got != "stop-xray,reset-plugins" {
			t.Fatalf("lifecycle events = %q, want stop-xray,reset-plugins", got)
		}
	})
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
