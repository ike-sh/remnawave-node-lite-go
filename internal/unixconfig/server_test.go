package unixconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
}

func (w *recordingWebhook) HandleXrayWebhook(payload xraywebhook.Payload) {
	w.calls++
	w.payload = payload
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
	processor := &recordingWebhook{}
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
		processor := &recordingWebhook{}
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

func TestUnixHandlerLimitRejectsInsteadOfQueueing(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	handler := limitUnixHandlers(1, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		t.Fatal("first unix handler did not start")
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("overload status = %d", response.Code)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first unix handler did not finish")
	}
}
