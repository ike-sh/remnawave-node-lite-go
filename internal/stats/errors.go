package stats

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
	errFailedSystemStats      = apiError{Code: "A010", Message: "Failed to get system stats", HTTPStatus: http.StatusInternalServerError}
	errFailedUsersStats       = apiError{Code: "A011", Message: "Failed to get users stats", HTTPStatus: http.StatusInternalServerError}
	errFailedInboundStats     = apiError{Code: "A012", Message: "Failed to get inbound stats", HTTPStatus: http.StatusInternalServerError}
	errFailedOutboundStats    = apiError{Code: "A013", Message: "Failed to get outbound stats", HTTPStatus: http.StatusInternalServerError}
	errFailedInboundsStats    = apiError{Code: "A015", Message: "Failed to get inbounds stats", HTTPStatus: http.StatusInternalServerError}
	errFailedOutboundsStats   = apiError{Code: "A016", Message: "Failed to get outbounds stats", HTTPStatus: http.StatusInternalServerError}
	errFailedCombinedStats    = apiError{Code: "A017", Message: "Failed to get combined stats", HTTPStatus: http.StatusInternalServerError}
	errFailedUserOnlineStatus = apiError{Code: "A009", Message: "Get Xray stats error", HTTPStatus: http.StatusInternalServerError}
	errFailedUserIPList       = apiError{Code: "A009", Message: "Get Xray stats error", HTTPStatus: http.StatusInternalServerError}
	errFailedUsersIPList      = apiError{Code: "A009", Message: "Get Xray stats error", HTTPStatus: http.StatusInternalServerError}
)

func writeAPIError(write writeJSONFn, w http.ResponseWriter, err apiError) {
	write(w, err.HTTPStatus, map[string]any{
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"message":   err.Message,
		"errorCode": err.Code,
	})
}
