package vision

import (
	"context"
	"encoding/json"
	"net/http"
)

type RouterProvider interface {
	RouterAddSrcIPRule(ctx context.Context, ip string, appendRule bool) error
	RouterRemoveRuleByIP(ctx context.Context, ip string) error
}

type Service struct {
	provider RouterProvider
}

func NewService(provider RouterProvider) *Service {
	return &Service{provider: provider}
}

type envelope[T any] struct {
	Response T `json:"response"`
}

type genericResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

type writeJSONFn func(w http.ResponseWriter, status int, value any)

func (s *Service) HandleBlockIP(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		IP       string `json:"ip"`
		Username string `json:"username"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	resp := genericResponse{Success: true, Error: nil}
	if s.provider != nil {
		if err := s.provider.RouterAddSrcIPRule(r.Context(), req.IP, true); err != nil {
			msg := err.Error()
			resp = genericResponse{Success: false, Error: &msg}
		}
	}

	write(w, http.StatusOK, envelope[genericResponse]{Response: resp})
}

func (s *Service) HandleUnblockIP(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		IP       string `json:"ip"`
		Username string `json:"username"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	resp := genericResponse{Success: true, Error: nil}
	if s.provider != nil {
		if err := s.provider.RouterRemoveRuleByIP(r.Context(), req.IP); err != nil {
			msg := err.Error()
			resp = genericResponse{Success: false, Error: &msg}
		}
	}

	write(w, http.StatusOK, envelope[genericResponse]{Response: resp})
}

func decodeBody(r *http.Request, target any) bool {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target) == nil
}

func writeError(write writeJSONFn, w http.ResponseWriter, message string) {
	write(w, http.StatusBadRequest, map[string]any{"message": message})
}
