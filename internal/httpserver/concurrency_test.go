package httpserver

import (
	"context"
	"encoding/json"
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
	mode, activeStarts, _ := server.xrayGate.snapshot()
	if mode != xrayLifecycleExclusive || activeStarts != 0 {
		t.Fatalf("%s lifecycle gate state = mode %d, starts %d; want exclusive", name, mode, activeStarts)
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

func TestBulkRouteLimiterWaitsBeforeReadingBody(t *testing.T) {
	t.Parallel()

	entered := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int64
	handler := limitBulkNodeRoutes(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
			httptest.NewRequest(http.MethodPost, "/node/handler/add-users", nil),
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

func TestXrayStartLimiterAllowsTwoAndBoundsAdditionalRequests(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	handler := limitXrayStartRoutes(2, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release
	}))

	completed := make(chan struct{}, 3)
	for range 2 {
		go func() {
			handler.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodPost, "/node/xray/start", nil),
			)
			completed <- struct{}{}
		}()
	}
	awaitTestSignal(t, entered, "first Xray start handler")
	awaitTestSignal(t, entered, "second Xray start handler")

	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	observed := make(chan struct{})
	thirdRequest := httptest.NewRequest(http.MethodPost, "/node/xray/start", nil).WithContext(
		&observedDoneContext{Context: waitCtx, observed: observed},
	)
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), thirdRequest)
		completed <- struct{}{}
	}()
	awaitTestSignal(t, observed, "third Xray start admission wait")
	select {
	case <-entered:
		t.Fatal("third Xray start entered while both start slots were occupied")
	default:
	}

	releaseOnce.Do(func() { close(release) })
	awaitTestSignal(t, entered, "third Xray start handler")
	for range 3 {
		awaitTestSignal(t, completed, "Xray start handler completion")
	}
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
	response := httptest.NewRecorder()
	done := make(chan any, 1)
	go func() {
		var panicValue any
		defer func() { done <- panicValue }()
		defer func() { panicValue = recover() }()
		handler.ServeHTTP(response, request)
	}()
	select {
	case panicValue := <-done:
		if panicValue != http.ErrAbortHandler {
			t.Fatalf("canceled admission panic = %#v, want http.ErrAbortHandler", panicValue)
		}
		if response.Body.Len() != 0 {
			t.Fatalf("canceled admission wrote response %q", response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled admission")
	}
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

func TestRequestTimeoutReturnsRetryableServiceUnavailable(t *testing.T) {
	t.Parallel()
	handler := withRequestTimeout(20*time.Millisecond, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	assertRetryableServiceUnavailable(t, response)
}

func TestResponseWriteTrackerHandlesImplicitAndDuplicateStatus(t *testing.T) {
	t.Parallel()

	t.Run("fresh recorder is still unwritten", func(t *testing.T) {
		response := httptest.NewRecorder()
		tracked := &responseWriteTracker{ResponseWriter: response}
		if tracked.wrote {
			t.Fatal("fresh response was marked written")
		}
	})

	t.Run("implicit success", func(t *testing.T) {
		response := httptest.NewRecorder()
		tracked := &responseWriteTracker{ResponseWriter: response}
		if _, err := tracked.Write([]byte("ok")); err != nil {
			t.Fatal(err)
		}
		if !tracked.wrote || response.Code != http.StatusOK || response.Body.String() != "ok" {
			t.Fatalf("tracked=%v status=%d body=%q", tracked.wrote, response.Code, response.Body.String())
		}
	})

	t.Run("first explicit status wins", func(t *testing.T) {
		response := httptest.NewRecorder()
		tracked := &responseWriteTracker{ResponseWriter: response}
		tracked.WriteHeader(http.StatusNoContent)
		tracked.WriteHeader(http.StatusInternalServerError)
		if !tracked.wrote || response.Code != http.StatusNoContent {
			t.Fatalf("tracked=%v status=%d", tracked.wrote, response.Code)
		}
	})
}

func TestAdmissionDeadlinesReturnRetryableServiceUnavailable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		build         func(http.Handler) http.Handler
		occupyingPath string
		waitingPath   string
	}{
		{
			name: "active handler gate",
			build: func(next http.Handler) http.Handler {
				return limitActiveHandlers(1, next)
			},
			occupyingPath: "/node/xray/healthcheck",
			waitingPath:   "/node/handler/add-user",
		},
		{
			name: "bulk body gate",
			build: func(next http.Handler) http.Handler {
				return limitBulkNodeRoutes(1, next)
			},
			occupyingPath: "/node/handler/add-users",
			waitingPath:   "/node/plugin/sync",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			limited := test.build(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				entered <- struct{}{}
				<-release
			}))

			firstDone := make(chan struct{})
			go func() {
				limited.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, test.occupyingPath, nil))
				close(firstDone)
			}()
			awaitTestSignal(t, entered, "occupying request")

			response := httptest.NewRecorder()
			withRequestTimeout(20*time.Millisecond, limited).ServeHTTP(
				response,
				httptest.NewRequest(http.MethodPost, test.waitingPath, nil),
			)
			assertRetryableServiceUnavailable(t, response)
			select {
			case <-entered:
				t.Fatal("timed-out request reached downstream handler")
			default:
			}

			releaseOnce.Do(func() { close(release) })
			awaitTestSignal(t, firstDone, "occupying request completion")
		})
	}
}

func assertRetryableServiceUnavailable(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	var body struct {
		StatusCode int    `json:"statusCode"`
		Message    string `json:"message"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v; body=%s", err, response.Body.String())
	}
	if body.StatusCode != http.StatusServiceUnavailable || body.Message == "" || body.Error != http.StatusText(http.StatusServiceUnavailable) {
		t.Fatalf("response body = %+v", body)
	}
}

func TestLifecycleGateDeadlineReturnsRetryableServiceUnavailable(t *testing.T) {
	t.Parallel()

	server := &Server{}
	if !server.acquireXrayLifecycle(context.Background()) {
		t.Fatal("failed to occupy lifecycle gate")
	}
	defer server.releaseXrayLifecycle()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/node/xray/stop", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.handleNodeRoutes(response, request)
	assertRetryableServiceUnavailable(t, response)
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
