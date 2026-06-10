package unixconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticProvider struct {
	config map[string]any
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
