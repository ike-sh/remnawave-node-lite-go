package nodehandler

import (
	"net/http"
	"time"
)

type apiError struct {
	Code       string
	Message    string
	HTTPStatus int
}

var (
	errInternalServer     = apiError{Code: "A001", Message: "Server error", HTTPStatus: http.StatusInternalServerError}
	errFailedInboundUsers = apiError{Code: "A014", Message: "Failed to get inbound users", HTTPStatus: http.StatusInternalServerError}
)

func writeHandlerAPIError(write writeJSONFn, w http.ResponseWriter, err apiError, message string) {
	if message == "" {
		message = err.Message
	}
	write(w, err.HTTPStatus, map[string]any{
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"message":   message,
		"errorCode": err.Code,
	})
}
