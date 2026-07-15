package nodehandler

import "github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"

var (
	errInternalServer     = nodeapi.ServiceError{Code: "A001", Message: "Server error", Status: 500}
	errFailedInboundUsers = nodeapi.ServiceError{Code: "A014", Message: "Failed to get inbound users", Status: 500}
)
