package unixconfig

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xraywebhook"
)

type staticProvider struct {
	config map[string]any
}

type recordingWebhook struct {
	calls   int
	payload xraywebhook.Payload
	accept  bool
}

func (w *recordingWebhook) HandleXrayWebhookContext(_ context.Context, payload xraywebhook.Payload) bool {
	w.calls++
	w.payload = payload
	return w.accept
}

func (p staticProvider) CurrentConfigJSON() []byte {
	if p.config == nil {
		return []byte("{}")
	}
	raw, err := json.Marshal(p.config)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func TestListenAndServeRejectsLiveSocketWithoutRemovingIt(t *testing.T) {
	path := unixSocketTestPath(t)
	existing, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on existing socket: %v", err)
	}
	existing.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = existing.Close()
		_ = os.Remove(path)
	})
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat existing socket: %v", err)
	}

	server := &Server{Path: path, Token: "token", Provider: staticProvider{}}
	err = server.ListenAndServe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already accepting connections") {
		t.Fatalf("ListenAndServe() error = %v", err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("live socket was replaced")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial original live socket: %v", err)
	}
	_ = conn.Close()
}

func TestListenAndServeReplacesStableStaleSocket(t *testing.T) {
	path := unixSocketTestPath(t)
	stale := leaveStaleUnixSocket(t, path)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	server := &Server{Path: path, Token: "token", Provider: staticProvider{}}
	go func() { done <- server.ListenAndServe(ctx) }()

	current := waitForLiveUnixSocket(t, path, stale)
	if current.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", current.Mode().Perm())
	}
	cancel()
	if err := waitForUnixServer(t, done); err != nil {
		t.Fatalf("ListenAndServe() = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("owned socket remained after shutdown: %v", err)
	}
}

func TestListenAndServeDoesNotRemoveReplacementSocket(t *testing.T) {
	path := unixSocketTestPath(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	server := &Server{Path: path, Token: "token", Provider: staticProvider{}}
	go func() { done <- server.ListenAndServe(ctx) }()
	waitForLiveUnixSocket(t, path, nil)

	displaced := path + ".owned"
	if err := os.Rename(path, displaced); err != nil {
		cancel()
		t.Fatalf("move owned socket: %v", err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		cancel()
		t.Fatalf("bind replacement socket: %v", err)
	}
	replacement.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = replacement.Close()
		_ = os.Remove(path)
		_ = os.Remove(displaced)
	})
	replacementInfo, err := os.Lstat(path)
	if err != nil {
		cancel()
		t.Fatalf("stat replacement socket: %v", err)
	}

	cancel()
	if err := waitForUnixServer(t, done); err != nil {
		t.Fatalf("ListenAndServe() = %v", err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("replacement socket was removed: %v", err)
	}
	if !os.SameFile(replacementInfo, current) {
		t.Fatal("replacement socket path changed during original server cleanup")
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial replacement socket: %v", err)
	}
	_ = conn.Close()
}

func TestConcurrentStaleSocketStartsKeepWinnerReachable(t *testing.T) {
	path := unixSocketTestPath(t)
	stale := leaveStaleUnixSocket(t, path)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	start := make(chan struct{})
	type serverResult struct {
		id  int
		err error
	}
	results := make(chan serverResult, 2)
	for id := 1; id <= 2; id++ {
		id := id
		server := &Server{Path: path, Token: "token", Provider: staticProvider{}}
		go func() {
			<-start
			results <- serverResult{id: id, err: server.ListenAndServe(ctx)}
		}()
	}
	close(start)

	var loser serverResult
	select {
	case loser = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent loser did not fail its non-blocking directory lock")
	}
	if loser.err == nil || !strings.Contains(loser.err.Error(), "lock unix socket directory") {
		t.Fatalf("server %d result = %v, want directory lock failure", loser.id, loser.err)
	}
	waitForLiveUnixSocket(t, path, stale)
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("dial winning server socket: %v", err)
	}
	_ = conn.Close()

	cancel()
	select {
	case winner := <-results:
		if winner.id == loser.id || winner.err != nil {
			t.Fatalf("winning server result = %#v, loser = %#v", winner, loser)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("winning unix server did not stop")
	}
}

func TestListenAndServeRejectsNonSocketAndSymlinkPaths(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := unixSocketTestPath(t)
			if kind == "file" {
				if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
					t.Fatalf("write sentinel: %v", err)
				}
			} else {
				target := path + ".target"
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create symlink: %v", err)
				}
			}

			server := &Server{Path: path, Token: "token", Provider: staticProvider{}}
			if err := server.ListenAndServe(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to replace") {
				t.Fatalf("ListenAndServe() error = %v", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("sentinel path was removed: %v", err)
			}
		})
	}
}

func unixSocketTestPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rnl-unix-")
	if err != nil {
		t.Fatalf("create unix socket test directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func leaveStaleUnixSocket(t *testing.T, path string) os.FileInfo {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("bind stale socket: %v", err)
	}
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("stat stale socket: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close stale socket: %v", err)
	}
	return info
}

func waitForLiveUnixSocket(t *testing.T, path string, previous os.FileInfo) os.FileInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeSocket != 0 && (previous == nil || !os.SameFile(previous, info)) {
			conn, dialErr := net.DialTimeout("unix", path, 20*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				return info
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("unix socket %q did not become ready", path)
	return nil
}

func waitForUnixServer(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("unix server did not stop")
		return nil
	}
}

func TestGetConfigRejectsInvalidToken(t *testing.T) {
	server := &Server{Token: "good", Provider: staticProvider{}}
	request := httptest.NewRequest(http.MethodGet, "/internal/get-config?token=bad", nil)
	response := httptest.NewRecorder()

	server.handleGetConfig(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", response.Code)
	}
}

func TestGetConfigReturnsEmptyObjectWhenMissing(t *testing.T) {
	server := &Server{Token: "good", Provider: staticProvider{}}
	request := httptest.NewRequest(http.MethodGet, "/internal/get-config?token=good", nil)
	response := httptest.NewRecorder()

	server.handleGetConfig(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty config, got %#v", body)
	}
}

func TestGetConfigAcceptsHeaderToken(t *testing.T) {
	server := &Server{Token: "good", Provider: staticProvider{}}
	request := httptest.NewRequest(http.MethodGet, "/internal/get-config", nil)
	request.Header.Set(InternalTokenHeader, "good")
	response := httptest.NewRecorder()

	server.handleGetConfig(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}

func TestGetConfigAllowsOwnerOnlyUnixSocket(t *testing.T) {
	server := &Server{Token: "good", Provider: staticProvider{}}
	request := httptest.NewRequest(http.MethodGet, "/internal/get-config", nil)
	response := httptest.NewRecorder()

	server.handleGetConfig(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 without query token (socket owner), got %d", response.Code)
	}
}

func TestGetConfigRejectsWhenTokenNotConfigured(t *testing.T) {
	server := &Server{Token: "", Provider: staticProvider{}}
	request := httptest.NewRequest(http.MethodGet, "/internal/get-config", nil)
	response := httptest.NewRecorder()

	server.handleGetConfig(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when token missing, got %d", response.Code)
	}
}

func TestGetConfigReturnsCurrentConfig(t *testing.T) {
	server := &Server{
		Token: "good",
		Provider: staticProvider{config: map[string]any{
			"inbounds": []any{},
		}},
	}
	request := httptest.NewRequest(http.MethodGet, "/internal/get-config?token=good", nil)
	response := httptest.NewRecorder()

	server.handleGetConfig(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := body["inbounds"]; !ok {
		t.Fatalf("expected current config, got %#v", body)
	}
}

func TestWebhookAcceptsOneBoundedOfficialPayload(t *testing.T) {
	processor := &recordingWebhook{accept: true}
	server := &Server{Token: "good", Provider: staticProvider{}, Webhook: processor}
	request := httptest.NewRequest(http.MethodPost, "/internal/webhook", strings.NewReader(`{
		"email":"user-1","level":0,"protocol":"vless","network":"tcp",
		"source":"tcp:203.0.113.10:443","destination":"198.51.100.1:443",
		"routeTarget":null,"originalTarget":null,"inboundTag":"in-1",
		"inboundName":null,"inboundLocal":null,"outboundTag":"direct","ts":123
	}`))
	request.Header.Set(InternalTokenHeader, "good")
	response := httptest.NewRecorder()

	server.handleWebhook(response, request)

	if response.Code != http.StatusOK || processor.calls != 1 {
		t.Fatalf("status = %d, webhook calls = %d", response.Code, processor.calls)
	}
	if processor.payload.Email == nil || *processor.payload.Email != "user-1" {
		t.Fatalf("payload = %#v", processor.payload)
	}
}

func TestWebhookRejectsInvalidOrOversizedPayloadBeforeProcessor(t *testing.T) {
	for _, body := range []string{
		`{"email":"missing-fields"}`,
		`{} {}`,
		strings.Repeat(" ", maxWebhookBodyBytes) + `{}`,
	} {
		processor := &recordingWebhook{accept: true}
		server := &Server{Token: "good", Provider: staticProvider{}, Webhook: processor}
		request := httptest.NewRequest(http.MethodPost, "/internal/webhook", strings.NewReader(body))
		request.Header.Set(InternalTokenHeader, "good")
		response := httptest.NewRecorder()

		server.handleWebhook(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.Code)
		}
		if processor.calls != 0 {
			t.Fatalf("invalid webhook reached processor: %q", body[:min(len(body), 64)])
		}
	}
}

func TestWebhookReturnsRetryableOverloadWhenProcessorRejects(t *testing.T) {
	t.Parallel()

	processor := &recordingWebhook{accept: false}
	server := &Server{Token: "good", Provider: staticProvider{}, Webhook: processor}
	request := httptest.NewRequest(http.MethodPost, "/internal/webhook", strings.NewReader(`{
		"email":"user-1","level":0,"protocol":"vless","network":"tcp",
		"source":"tcp:203.0.113.10:443","destination":"198.51.100.1:443",
		"routeTarget":null,"originalTarget":null,"inboundTag":"in-1",
		"inboundName":null,"inboundLocal":null,"outboundTag":"direct","ts":123
	}`))
	request.Header.Set(InternalTokenHeader, "good")
	response := httptest.NewRecorder()

	server.handleWebhook(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("Retry-After") != "1" || processor.calls != 1 {
		t.Fatalf("headers=%v calls=%d", response.Header(), processor.calls)
	}
}

func TestUnixHandlerLimitWaitsForCapacity(t *testing.T) {
	t.Parallel()
	entered := make(chan int, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	var calls atomic.Int64
	handler := limitUnixHandlers(1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := int(calls.Add(1))
		entered <- call
		if call == 1 {
			<-release
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(firstDone)
	}()
	if got := <-entered; got != 1 {
		t.Fatalf("first unix handler call = %d", got)
	}

	response := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("second unix request completed while the handler slot was occupied")
	case <-time.After(20 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-entered:
		if got != 2 {
			t.Fatalf("second unix handler call = %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second unix handler did not start")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second unix handler did not finish")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("second unix response = %d", response.Code)
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first unix handler did not finish")
	}
}

func TestUnixHandlerLimitReservesCapacityForConfig(t *testing.T) {
	t.Parallel()

	webhookEntered := make(chan struct{}, 1)
	configEntered := make(chan struct{}, 1)
	releaseWebhook := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseWebhook) }) })
	handler := limitUnixHandlers(2, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/webhook" {
			webhookEntered <- struct{}{}
			<-releaseWebhook
			return
		}
		configEntered <- struct{}{}
	}))

	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/internal/webhook", nil))
		close(firstDone)
	}()
	select {
	case <-webhookEntered:
	case <-time.After(time.Second):
		t.Fatal("first webhook did not enter")
	}

	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/internal/webhook", nil))
		close(secondDone)
	}()
	select {
	case <-webhookEntered:
		t.Fatal("second webhook consumed the config reserve")
	case <-time.After(20 * time.Millisecond):
	}

	configDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/internal/get-config", nil))
		close(configDone)
	}()
	select {
	case <-configEntered:
	case <-time.After(time.Second):
		t.Fatal("config request could not use its reserved handler slot")
	}
	select {
	case <-configDone:
	case <-time.After(time.Second):
		t.Fatal("config request did not finish")
	}

	releaseOnce.Do(func() { close(releaseWebhook) })
	select {
	case <-webhookEntered:
	case <-time.After(time.Second):
		t.Fatal("queued webhook did not enter after capacity was released")
	}
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first webhook did not finish")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second webhook did not finish")
	}
}

func TestUnixRequestTimeoutCoversHandlerAdmission(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	handler := withUnixRequestTimeout(20*time.Millisecond, limitUnixHandlers(1, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		entered <- struct{}{}
		<-release
	})))

	firstDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(firstDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first unix handler did not start")
	}

	secondResponse := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(secondResponse, httptest.NewRequest(http.MethodGet, "/", nil))
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("request timeout did not cancel handler admission")
	}
	if secondResponse.Code != http.StatusServiceUnavailable || secondResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("timed-out admission response = %d headers=%v", secondResponse.Code, secondResponse.Header())
	}
	select {
	case <-entered:
		t.Fatal("timed-out unix request reached the downstream handler")
	default:
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first unix handler did not finish")
	}
}
