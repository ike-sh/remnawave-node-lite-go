package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

type observedReader struct {
	reads atomic.Int64
}

func (r *observedReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.EOF
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

func TestLimitActiveHandlersWaitsForCapacity(t *testing.T) {
	t.Parallel()

	entered := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int64
	handler := limitActiveHandlers(1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := int(calls.Add(1))
		entered <- call
		if call == 1 {
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil))
		close(firstDone)
	}()
	awaitTestSignal(t, signalForCall(entered, 1), "first handler")

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil)
	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("second request completed while the only handler slot was occupied")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	awaitTestSignal(t, signalForCall(entered, 2), "second handler")
	awaitTestSignal(t, secondDone, "second handler completion")
	if response.Code != http.StatusNoContent {
		t.Fatalf("second response = %d, want 204", response.Code)
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first handler did not finish")
	}
}

func TestHeavyRouteLimiterWaitsBeforeReadingBody(t *testing.T) {
	t.Parallel()

	entered := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int64
	handler := limitHeavyNodeRoutes(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		call := int(calls.Add(1))
		entered <- call
		if call == 1 {
			<-release
		}
	}))

	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "/node/xray/start", nil),
		)
		close(firstDone)
	}()
	awaitTestSignal(t, signalForCall(entered, 1), "first heavy route")

	body := &observedReader{}
	request := httptest.NewRequest(http.MethodPost, "/node/plugin/sync", body)
	response := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("second heavy request completed while the heavy slot was occupied")
	case <-time.After(20 * time.Millisecond):
	}
	if body.reads.Load() != 0 {
		t.Fatalf("waiting body was read %d times", body.reads.Load())
	}

	releaseOnce.Do(func() { close(release) })
	awaitTestSignal(t, signalForCall(entered, 2), "second heavy route")
	awaitTestSignal(t, secondDone, "second heavy route completion")
	awaitTestSignal(t, firstDone, "first heavy route completion")
}

func TestActiveHandlerLimitReservesCapacityForMutations(t *testing.T) {
	t.Parallel()

	readEntered := make(chan struct{}, 2)
	mutationEntered := make(chan struct{}, 1)
	releaseReads := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseReads) }) })
	handler := limitActiveHandlers(2, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if route, _ := lookupNodeRoute(r.Method, r.URL.Path); nodeRouteIsReadOnly(route) {
			readEntered <- struct{}{}
			<-releaseReads
			return
		}
		mutationEntered <- struct{}{}
	}))

	firstReadDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil))
		close(firstReadDone)
	}()
	awaitTestSignal(t, readEntered, "first read-only handler")

	secondReadDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/node/stats/get-system-stats", nil))
		close(secondReadDone)
	}()
	select {
	case <-readEntered:
		t.Fatal("second read-only request consumed the mutation reserve")
	case <-time.After(20 * time.Millisecond):
	}

	mutationDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/node/handler/add-user", nil))
		close(mutationDone)
	}()
	awaitTestSignal(t, mutationEntered, "reserved mutation handler")
	awaitTestSignal(t, mutationDone, "reserved mutation completion")

	releaseOnce.Do(func() { close(releaseReads) })
	awaitTestSignal(t, readEntered, "queued read-only handler")
	awaitTestSignal(t, firstReadDone, "first read-only completion")
	awaitTestSignal(t, secondReadDone, "second read-only completion")
}

func TestHandlerAdmissionStopsWaitingWhenRequestIsCanceled(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	handler := limitActiveHandlers(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
	go handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil),
	)
	awaitTestSignal(t, entered, "occupying handler")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/node/handler/add-user", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	awaitTestSignal(t, done, "canceled admission")
}

func signalForCall(c <-chan int, want int) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		if got := <-c; got == want {
			close(done)
		}
	}()
	return done
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
