package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type stubRouter struct{}

func (stubRouter) RouterAddSrcIPRule(ctx context.Context, ip string, appendRule bool) error {
	return nil
}

func (stubRouter) RouterRemoveRuleByIP(ctx context.Context, ip string) error {
	return nil
}

func TestHandleBlockIPRejectsInvalidIP(t *testing.T) {
	service := NewService(stubRouter{})
	body, _ := json.Marshal(map[string]string{"ip": "not-an-ip", "username": "u1"})
	req := httptest.NewRequest(http.MethodPost, "/vision/block-ip", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	service.HandleBlockIP(rec, req, func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
