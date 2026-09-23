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
	errInternalServer = apiError{Code: "A001", Message: "Server error", HTTPStatus: http.StatusInternalServerError}
)

func writeHandlerAPIError(write writeJSONFn, w http.ResponseWriter, err apiError, message string) {
	if message == "" {
		message = err.Message
	}
	write(w, err.HTTPStatus, map[string]any{
		"timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"message":   message,
		"errorCode": err.Code,
	})
}
