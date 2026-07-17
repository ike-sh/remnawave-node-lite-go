package unixconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
