package httpserver

import (
	"net/http"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodehandler"
)

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.AddUserRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.handlerService.AddUser(r.Context(), mapAddUserRequest(request))
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.RemoveUserRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.handlerService.RemoveUser(r.Context(), nodehandler.RemoveUserRequest{
		Username:  *request.Username,
		VlessUUID: *request.HashData.VlessUUID,
	})
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleGetInboundUsersCount(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.TagRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.handlerService.GetInboundUsersCount(r.Context(), *request.Tag)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleGetInboundUsers(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.TagRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.handlerService.GetInboundUsers(r.Context(), *request.Tag)
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleAddUsers(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.AddUsersRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	response, err := s.handlerService.AddUsers(r.Context(), mapAddUsersRequest(request))
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleRemoveUsers(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.RemoveUsersRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	users := make([]nodehandler.RemoveUsersItem, 0, len(*request.Users))
	for _, user := range *request.Users {
		users = append(users, nodehandler.RemoveUsersItem{
			UserID:   *user.UserID,
			HashUUID: *user.HashUUID,
		})
	}
	response, err := s.handlerService.RemoveUsers(r.Context(), nodehandler.RemoveUsersRequest{Users: users})
	writeNodeResult(w, r, response, err)
}

func (s *Server) handleDropUsersConnections(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.DropUsersConnectionsRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	writeNodeResponse(w, s.handlerService.DropUsersConnections(r.Context(), *request.UserIDs))
}

func (s *Server) handleDropIPs(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.DropIPsRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	writeNodeResponse(w, s.handlerService.DropIPs(*request.IPs))
}

func mapAddUserRequest(request nodeapi.AddUserRequest) nodehandler.AddUserRequest {
	items := make([]nodehandler.AddUserItem, 0, len(*request.Data))
	for _, item := range *request.Data {
		items = append(items, nodehandler.AddUserItem{
			Type:       stringValue(item.Type),
			Tag:        stringValue(item.Tag),
			Username:   stringValue(item.Username),
			Password:   stringValue(item.Password),
			UUID:       stringValue(item.UUID),
			Flow:       stringValue(item.Flow),
			CipherType: intValue(item.CipherType),
			IVCheck:    boolValue(item.IVCheck),
		})
	}

	var previous *string
	if request.HashData.PrevVlessUUID.Present {
		value := request.HashData.PrevVlessUUID.Value
		previous = &value
	}
	return nodehandler.AddUserRequest{
		Data: items,
		HashData: nodehandler.AddUserHashData{
			VlessUUID:     *request.HashData.VlessUUID,
			PrevVlessUUID: previous,
		},
	}
}

func mapAddUsersRequest(request nodeapi.AddUsersRequest) nodehandler.AddUsersRequest {
	users := make([]nodehandler.BatchUser, 0, len(*request.Users))
	for _, user := range *request.Users {
		inbounds := make([]nodehandler.BatchInbound, 0, len(*user.InboundData))
		for _, inbound := range *user.InboundData {
			inbounds = append(inbounds, nodehandler.BatchInbound{
				Type: stringValue(inbound.Type),
				Tag:  stringValue(inbound.Tag),
				Flow: stringValue(inbound.Flow),
			})
		}
		users = append(users, nodehandler.BatchUser{
			InboundData: inbounds,
			UserData: nodehandler.BatchUserData{
				UserID:         *user.UserData.UserID,
				HashUUID:       *user.UserData.HashUUID,
				VlessUUID:      *user.UserData.VlessUUID,
				TrojanPassword: *user.UserData.TrojanPassword,
				SSPassword:     *user.UserData.SSPassword,
			},
		})
	}
	return nodehandler.AddUsersRequest{
		AffectedInboundTags: append([]string(nil), (*request.AffectedInboundTags)...),
		Users:               users,
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func boolValue(value *bool) bool {
	return value != nil && *value
}
