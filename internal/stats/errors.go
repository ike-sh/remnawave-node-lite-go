package stats

import "github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"

var (
	errFailedSystemStats    = nodeapi.ServiceError{Code: "A010", Message: "Failed to get system stats", Status: 500}
	errFailedUsersStats     = nodeapi.ServiceError{Code: "A011", Message: "Failed to get users stats", Status: 500}
	errFailedInboundStats   = nodeapi.ServiceError{Code: "A012", Message: "Failed to get inbound stats", Status: 500}
	errFailedOutboundStats  = nodeapi.ServiceError{Code: "A013", Message: "Failed to get outbound stats", Status: 500}
	errFailedInboundsStats  = nodeapi.ServiceError{Code: "A015", Message: "Failed to get inbounds stats", Status: 500}
	errFailedOutboundsStats = nodeapi.ServiceError{Code: "A016", Message: "Failed to get outbounds stats", Status: 500}
	errFailedCombinedStats  = nodeapi.ServiceError{Code: "A017", Message: "Failed to get combined stats", Status: 500}
)
