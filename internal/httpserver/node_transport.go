package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

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
