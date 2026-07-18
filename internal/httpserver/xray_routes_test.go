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
	"time"

	contractspec "github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/system"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xray"
)

type recordingXrayController struct {
	startCalls  atomic.Int64
	stopCalls   atomic.Int64
	healthCalls atomic.Int64
	request     xray.StartRequest
	events      *[]string
	startOnce   sync.Once
	startEvent  chan struct{}
	startWait   <-chan struct{}
	stopResult  *xray.StopResponse
}

type concurrentStartController struct {
	calls        atomic.Int64
	active       atomic.Bool
	firstEntered chan struct{}
	releaseFirst <-chan struct{}
}

func (x *concurrentStartController) Start(ctx context.Context, _ xray.StartRequest) xray.StartResponse {
	x.calls.Add(1)
	if !x.active.CompareAndSwap(false, true) {
		message := "Request already in progress"
		return xray.StartResponse{
			Error:           &message,
			NodeInformation: xray.NodeInformation{},
			System:          system.GetSnapshot(),
		}
	}
	defer x.active.Store(false)
	close(x.firstEntered)
	select {
	case <-x.releaseFirst:
	case <-ctx.Done():
		message := ctx.Err().Error()
		return xray.StartResponse{Error: &message, System: system.GetSnapshot()}
	}
	return xray.StartResponse{
		IsStarted:       true,
		NodeInformation: xray.NodeInformation{},
		System:          system.GetSnapshot(),
	}
}

func (*concurrentStartController) Stop() xray.StopResponse { return xray.StopResponse{IsStopped: true} }
func (*concurrentStartController) Health() xray.HealthResponse {
	return xray.HealthResponse{}
}

func (x *recordingXrayController) Start(ctx context.Context, request xray.StartRequest) xray.StartResponse {
	x.startCalls.Add(1)
	if x.events != nil {
		*x.events = append(*x.events, "start-xray")
	}
	if x.startEvent != nil {
		x.startOnce.Do(func() { close(x.startEvent) })
	}
	if x.startWait != nil {
		select {
		case <-x.startWait:
		case <-ctx.Done():
			message := ctx.Err().Error()
			return xray.StartResponse{Error: &message, System: system.GetSnapshot()}
		}
	}
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
	x.stopCalls.Add(1)
	if x.events != nil {
		*x.events = append(*x.events, "stop-xray")
	}
	if x.stopResult != nil {
		return *x.stopResult
	}
	return xray.StopResponse{IsStopped: true}
}

func (x *recordingXrayController) Health() xray.HealthResponse {
	x.healthCalls.Add(1)
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
			req := newJSONRequest(http.MethodPost, "/node/xray/start", strings.NewReader(test.body))
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
	req := newJSONRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest))
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

func TestConcurrentXrayStartsReachManagerThroughRequestMiddleware(t *testing.T) {
	t.Parallel()

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseFirst) }) })
	manager := &concurrentStartController{firstEntered: firstEntered, releaseFirst: releaseFirst}
	server := &Server{manager: manager}
	handler := requireKnownNodeRoute(withRequestTimeout(maxRequestDuration, server.nodeRequestHandler(defaultMaxHandlers)))
	route, _ := contractspec.FindRouteByPath("/node/xray/start")

	firstResponse := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(firstResponse, newJSONRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest)))
	}()
	awaitTestSignal(t, firstEntered, "first Xray start")

	secondResponse := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		handler.ServeHTTP(secondResponse, newJSONRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest)))
	}()
	awaitTestSignal(t, secondDone, "concurrent Xray start response")
	if manager.calls.Load() != 2 {
		t.Fatalf("manager start calls = %d, want 2", manager.calls.Load())
	}
	if secondResponse.Code != http.StatusOK ||
		!strings.Contains(secondResponse.Body.String(), `"isStarted":false`) ||
		!strings.Contains(secondResponse.Body.String(), `"error":"Request already in progress"`) {
		t.Fatalf("concurrent start response = %d %s", secondResponse.Code, secondResponse.Body.String())
	}
	if err := contractspec.ValidateResponse(route.Path, secondResponse.Body.Bytes()); err != nil {
		t.Fatalf("concurrent response violates official schema: %v\n%s", err, secondResponse.Body.Bytes())
	}

	releaseOnce.Do(func() { close(releaseFirst) })
	awaitTestSignal(t, firstDone, "first Xray start response")
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first start response = %d %s", firstResponse.Code, firstResponse.Body.String())
	}
}

func TestXrayStopResetsPluginsAfterStoppingProcess(t *testing.T) {
	t.Parallel()

	route, _ := contractspec.FindRouteByPath("/node/xray/stop")
	events := []string{}
	manager := &recordingXrayController{events: &events}
	plugins := &recordingPluginController{events: &events}
	server := &Server{manager: manager, pluginService: plugins}
	req := httptest.NewRequest(route.Method, route.Path, nil)
	rec := httptest.NewRecorder()

	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if err := contractspec.ValidateResponse(route.Path, rec.Body.Bytes()); err != nil {
		t.Fatalf("response violates official schema: %v\n%s", err, rec.Body.Bytes())
	}
	if manager.stopCalls.Load() != 1 || plugins.calls.Load() != 1 {
		t.Fatalf("calls: stop=%d reset=%d", manager.stopCalls.Load(), plugins.calls.Load())
	}
	if len(events) != 2 || events[0] != "stop-xray" || events[1] != "reset-plugins" {
		t.Fatalf("stop order = %#v", events)
	}
}

func TestXrayStopFailurePreservesPluginRules(t *testing.T) {
	t.Parallel()

	stopped := xray.StopResponse{IsStopped: false}
	manager := &recordingXrayController{stopResult: &stopped}
	plugins := &recordingPluginController{}
	server := &Server{manager: manager, pluginService: plugins}
	req := httptest.NewRequest(http.MethodGet, "/node/xray/stop", nil)
	rec := httptest.NewRecorder()

	server.handleNodeRoutes(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"isStopped":false`) {
		t.Fatalf("stop response = %d %s", rec.Code, rec.Body.String())
	}
	if plugins.calls.Load() != 0 {
		t.Fatalf("plugin reset calls = %d, want 0", plugins.calls.Load())
	}
}

func TestXrayStartWaitsUntilStopFinishesPluginReset(t *testing.T) {
	t.Parallel()

	resetStarted := make(chan struct{})
	releaseReset := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseReset) }) })
	startCalled := make(chan struct{})
	manager := &recordingXrayController{startEvent: startCalled}
	plugins := &recordingPluginController{resetStart: resetStarted, resetWait: releaseReset}
	server := &Server{manager: manager, pluginService: plugins}

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		server.handleNodeRoutes(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/node/xray/stop", nil),
		)
	}()
	select {
	case <-resetStarted:
	case <-time.After(time.Second):
		t.Fatal("stop did not enter plugin reset")
	}
	assertLifecycleGateHeld(t, server, "xray stop")

	route, _ := contractspec.FindRouteByPath("/node/xray/start")
	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	observed := make(chan struct{})
	startRequest := newJSONRequest(route.Method, route.Path, bytes.NewReader(route.ValidRequest)).WithContext(
		&observedDoneContext{Context: waitCtx, observed: observed},
	)
	startDone := make(chan struct{})
	go func() {
		defer close(startDone)
		server.handleNodeRoutes(
			httptest.NewRecorder(),
			startRequest,
		)
	}()
	awaitTestSignal(t, observed, "xray start lifecycle wait")
	assertLifecycleGateHeld(t, server, "xray stop")
	if manager.startCalls.Load() != 0 {
		t.Fatal("xray start ran before plugin reset completed")
	}
	releaseOnce.Do(func() { close(releaseReset) })
	select {
	case <-startCalled:
	case <-time.After(time.Second):
		t.Fatal("xray start did not run after plugin reset")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("xray stop did not finish")
	}
	select {
	case <-startDone:
	case <-time.After(time.Second):
		t.Fatal("xray start did not finish")
	}
}

func TestXrayStopWaitsUntilStartFinishes(t *testing.T) {
	t.Parallel()

	events := []string{}
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseStart) }) })
	manager := &recordingXrayController{
		events:     &events,
		startEvent: startEntered,
		startWait:  releaseStart,
	}
	plugins := &recordingPluginController{events: &events}
	server := &Server{manager: manager, pluginService: plugins}
	startRoute, _ := contractspec.FindRouteByPath("/node/xray/start")
	startResult := serveNodeRouteAsync(server, newJSONRequest(
		startRoute.Method,
		startRoute.Path,
		bytes.NewReader(startRoute.ValidRequest),
	))
	awaitTestSignal(t, startEntered, "Xray start")
	if mode, starts, _ := server.xrayGate.snapshot(); mode != xrayLifecycleStarts || starts != 1 {
		t.Fatalf("lifecycle gate = mode %d, starts %d; want starts/1", mode, starts)
	}

	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	observed := make(chan struct{})
	stopRoute, _ := contractspec.FindRouteByPath("/node/xray/stop")
	stopRequest := httptest.NewRequest(stopRoute.Method, stopRoute.Path, nil).WithContext(
		&observedDoneContext{Context: waitCtx, observed: observed},
	)
	stopResult := serveNodeRouteAsync(server, stopRequest)
	awaitTestSignal(t, observed, "Xray stop lifecycle wait")
	if manager.stopCalls.Load() != 0 || plugins.calls.Load() != 0 {
		t.Fatalf("stop advanced during start: stop=%d reset=%d", manager.stopCalls.Load(), plugins.calls.Load())
	}

	releaseOnce.Do(func() { close(releaseStart) })
	for name, result := range map[string]<-chan asyncRouteResult{
		"Xray start": startResult,
		"Xray stop":  stopResult,
	} {
		value := awaitRouteResult(t, result, name)
		if value.panicValue != nil || value.response.Code != http.StatusOK {
			t.Fatalf("%s result: panic=%v status=%d body=%s", name, value.panicValue, value.response.Code, value.response.Body.String())
		}
	}
	if got := strings.Join(events, ","); got != "start-xray,stop-xray,reset-plugins" {
		t.Fatalf("lifecycle events = %q", got)
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
	req := newJSONRequest(http.MethodPost, "/node/xray/start", strings.NewReader(body))
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
