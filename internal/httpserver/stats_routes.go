package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func (s *Server) handleStatsGetUserOnlineStatus(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.UsernameRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	writeNodeResponse(w, s.statsService.GetUserOnlineStatus(r.Context(), *request.Username))
}

func (s *Server) handleStatsGetSystemStats(w http.ResponseWriter, r *http.Request) {
	response, err := s.statsService.GetSystemStats(r.Context())
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetUsersStats(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.ResetRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.statsService.GetUsersStats(r.Context(), *request.Reset)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetInboundStats(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.TagResetRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.statsService.GetInboundStats(r.Context(), *request.Tag, *request.Reset)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetOutboundStats(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.TagResetRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.statsService.GetOutboundStats(r.Context(), *request.Tag, *request.Reset)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetAllInboundsStats(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.ResetRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.statsService.GetAllInboundsStats(r.Context(), *request.Reset)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetAllOutboundsStats(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.ResetRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.statsService.GetAllOutboundsStats(r.Context(), *request.Reset)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetCombinedStats(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.ResetRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.statsService.GetCombinedStats(r.Context(), *request.Reset)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleStatsGetUserIPList(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.UserIDRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	writeNodeResponse(w, s.statsService.GetUserIPList(r.Context(), *request.UserID))
}

func (s *Server) handleStatsGetUsersIPList(w http.ResponseWriter, r *http.Request) {
	writeNodeResponse(w, s.statsService.GetUsersIPList(r.Context()))
}

func decodeNodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	validation := nodeapi.DecodeJSON(r.Body, target)
	if validation == nil {
		return true
	}
	writeJSON(w, validation.StatusCode, validation)
	return false
}

func writeNodeResult(w http.ResponseWriter, r *http.Request, response any, err error) {
	if err == nil {
		writeNodeResponse(w, response)
		return
	}

	serviceError, ok := nodeapi.AsServiceError(err)
	if !ok {
		slog.Error("unclassified Node API error", "error", err, "path", r.URL.Path)
		serviceError = nodeapi.ServiceError{
			Status:  http.StatusInternalServerError,
			Code:    "E000",
			Message: "Internal server error",
		}
	}
	writeJSON(w, serviceError.Status, nodeapi.NewApplicationError(r.URL.RequestURI(), serviceError))
}

func writeNodeResponse(w http.ResponseWriter, response any) {
	writeJSON(w, http.StatusOK, envelope[any]{Response: response})
}
