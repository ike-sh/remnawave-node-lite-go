package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type asyncRouteResult struct {
	response   *httptest.ResponseRecorder
	panicValue any
}

func serveNodeRouteAsync(server *Server, request *http.Request) <-chan asyncRouteResult {
	done := make(chan asyncRouteResult, 1)
	go func() {
		response := httptest.NewRecorder()
		var panicValue any
		func() {
			defer func() { panicValue = recover() }()
			server.handleNodeRoutes(response, request)
		}()
		done <- asyncRouteResult{response: response, panicValue: panicValue}
	}()
	return done
}

func awaitTestSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func awaitRouteResult(t *testing.T, result <-chan asyncRouteResult, name string) asyncRouteResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return asyncRouteResult{}
	}
}

func assertLifecycleGateHeld(t *testing.T, server *Server, name string) {
	t.Helper()
	if server.xrayGate == nil {
		t.Fatalf("%s lifecycle gate is nil", name)
	}
	if capacity, held := cap(server.xrayGate), len(server.xrayGate); capacity != 1 || held != 1 {
		t.Fatalf("%s lifecycle gate state = %d/%d, want 1/1", name, held, capacity)
	}
}

func TestLimitActiveHandlersRejectsInsteadOfQueueing(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	handler := limitActiveHandlers(1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(firstDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	started := time.Now()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("overload response = %d headers=%v", response.Code, response.Header())
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("overload request queued for %s", elapsed)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first handler did not finish")
	}
}

func TestLowMemoryServerCapacityIsConservative(t *testing.T) {
	t.Parallel()
	connections, handlers := serverCapacity(true)
	if connections != lowMemoryConnections || handlers != lowMemoryHandlers || handlers >= connections {
		t.Fatalf("low-memory capacity = %d connections / %d handlers", connections, handlers)
	}
}

func TestRequestTimeoutCancelsHandlerContext(t *testing.T) {
	t.Parallel()
	handler := withRequestTimeout(20*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestUnknownRouteAbortsBeforeSaturatedLimiter(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	limited := limitActiveHandlers(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
	handler := requireKnownNodeRoute(limited)
	go handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil),
	)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("known route did not occupy handler slot")
	}

	defer func() {
		releaseOnce.Do(func() { close(release) })
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("unknown route panic = %#v, want http.ErrAbortHandler", recovered)
		}
	}()
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/node/xray/healthcheck", nil),
	)
}
