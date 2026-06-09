package nodehandler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"remnawave-node-lite-go/internal/connections"
	"remnawave-node-lite-go/internal/xtls"
)

type Provider interface {
	AddInboundTag(tag string)
	InboundTags() []string
	AddUserToInboundHash(inboundTag, userUUID string)
	RemoveUserFromInboundHash(inboundTag, userUUID string)
	GetUserIPList(ctx context.Context, userID string, reset bool) ([]xtls.IPEntry, error)
	HandlerRemoveUser(ctx context.Context, tag, username string) xtls.HandlerResult
	HandlerAddVlessUser(ctx context.Context, tag, username, uuid, flow string, level uint32) xtls.HandlerResult
	HandlerAddTrojanUser(ctx context.Context, tag, username, password string, level uint32) xtls.HandlerResult
	HandlerAddShadowsocksUser(ctx context.Context, tag, username, password string, cipherType int, ivCheck bool, level uint32) xtls.HandlerResult
	HandlerAddShadowsocks2022User(ctx context.Context, tag, username, key string, level uint32) xtls.HandlerResult
	HandlerAddHysteriaUser(ctx context.Context, tag, username, auth string, level uint32) xtls.HandlerResult
	HandlerGetInboundUsers(ctx context.Context, tag string) ([]xtls.InboundUser, xtls.HandlerResult)
	HandlerGetInboundUsersCount(ctx context.Context, tag string) (int64, xtls.HandlerResult)
}

type Service struct {
	provider Provider
	dropper  *connections.Dropper
}

func NewService(provider Provider, dropper *connections.Dropper) *Service {
	return &Service{provider: provider, dropper: dropper}
}

type envelope[T any] struct {
	Response T `json:"response"`
}

type genericResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

type writeJSONFn func(w http.ResponseWriter, status int, value any)

func (s *Service) HandleAddUser(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	defer recoverHandler(write, w)
	var req addUserRequest
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}
	if len(req.Data) == 0 {
		writeError(write, w, "data is required")
		return
	}

	for _, item := range req.Data {
		s.provider.AddInboundTag(item.Tag)
	}

	hashUUID := req.HashData.VlessUUID
	if req.HashData.PrevVlessUUID != nil && *req.HashData.PrevVlessUUID != "" {
		hashUUID = *req.HashData.PrevVlessUUID
	}

	username := req.Data[0].Username
	if username != "" {
		for _, tag := range s.provider.InboundTags() {
			s.provider.HandlerRemoveUser(r.Context(), tag, username)
			s.provider.RemoveUserFromInboundHash(tag, hashUUID)
		}
	}

	results := make([]xtls.HandlerResult, 0, len(req.Data))
	for _, item := range req.Data {
		result := s.addSingleUser(r.Context(), item)
		if result.OK {
			s.provider.AddUserToInboundHash(item.Tag, req.HashData.VlessUUID)
		}
		results = append(results, result)
	}

	write(w, http.StatusOK, envelope[genericResponse]{Response: aggregateResults(results)})
}

func (s *Service) HandleRemoveUser(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	defer recoverHandler(write, w)
	var req removeUserRequest
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	tags := s.provider.InboundTags()
	if len(tags) == 0 {
		write(w, http.StatusOK, envelope[genericResponse]{Response: genericResponse{Success: true, Error: nil}})
		return
	}

	userIPs := collectUserIPs(r.Context(), s.provider, req.Username)
	results := make([]xtls.HandlerResult, 0, len(tags))
	for _, tag := range tags {
		results = append(results, s.provider.HandlerRemoveUser(r.Context(), tag, req.Username))
		s.provider.RemoveUserFromInboundHash(tag, req.HashData.VlessUUID)
	}
	s.dropIPs(userIPs)
	write(w, http.StatusOK, envelope[genericResponse]{Response: aggregateResults(results)})
}

func (s *Service) HandleAddUsers(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	defer recoverHandler(write, w)
	var req addUsersRequest
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	for _, tag := range req.AffectedInboundTags {
		s.provider.AddInboundTag(tag)
	}

	for _, user := range req.Users {
		for _, inbound := range user.InboundData {
			s.provider.AddInboundTag(inbound.Tag)
		}
		for _, tag := range s.provider.InboundTags() {
			s.provider.HandlerRemoveUser(r.Context(), tag, user.UserData.UserID)
			s.provider.RemoveUserFromInboundHash(tag, user.UserData.HashUUID)
		}
		for _, inbound := range user.InboundData {
			result := s.addBatchUser(r.Context(), inbound, user.UserData)
			if result.OK {
				s.provider.AddUserToInboundHash(inbound.Tag, user.UserData.VlessUUID)
			}
		}
	}

	// Match upstream addUsers: always success:true on HTTP 200 (individual failures are silent).
	write(w, http.StatusOK, envelope[genericResponse]{Response: genericResponse{Success: true, Error: nil}})
}

func (s *Service) HandleRemoveUsers(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	defer recoverHandler(write, w)
	var req removeUsersRequest
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	tags := s.provider.InboundTags()
	if len(tags) == 0 {
		write(w, http.StatusOK, envelope[genericResponse]{Response: genericResponse{Success: true, Error: nil}})
		return
	}

	results := make([]xtls.HandlerResult, 0, len(req.Users)*len(tags))
	for _, user := range req.Users {
		userIPs := collectUserIPs(r.Context(), s.provider, user.UserID)
		userTags := s.provider.InboundTags()
		for _, tag := range userTags {
			results = append(results, s.provider.HandlerRemoveUser(r.Context(), tag, user.UserID))
			s.provider.RemoveUserFromInboundHash(tag, user.HashUUID)
		}
		s.dropIPs(userIPs)
	}

	write(w, http.StatusOK, envelope[genericResponse]{Response: aggregateResults(results)})
}

func (s *Service) HandleGetInboundUsersCount(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req tagRequest
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	if s.provider == nil || req.Tag == "" {
		write(w, http.StatusOK, envelope[struct {
			Count int64 `json:"count"`
		}]{Response: struct {
			Count int64 `json:"count"`
		}{Count: 0}})
		return
	}
	count, result := s.provider.HandlerGetInboundUsersCount(r.Context(), req.Tag)
	if !result.OK {
		writeHandlerAPIError(write, w, errFailedInboundUsers, handlerErrorMessage(result.Message, errFailedInboundUsers.Message))
		return
	}

	write(w, http.StatusOK, envelope[struct {
		Count int64 `json:"count"`
	}]{Response: struct {
		Count int64 `json:"count"`
	}{Count: count}})
}

func (s *Service) HandleGetInboundUsers(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req tagRequest
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	if s.provider == nil || req.Tag == "" {
		write(w, http.StatusOK, envelope[struct {
			Users []xtls.InboundUser `json:"users"`
		}]{Response: struct {
			Users []xtls.InboundUser `json:"users"`
		}{Users: []xtls.InboundUser{}}})
		return
	}
	users, result := s.provider.HandlerGetInboundUsers(r.Context(), req.Tag)
	if !result.OK {
		writeHandlerAPIError(write, w, errFailedInboundUsers, handlerErrorMessage(result.Message, errFailedInboundUsers.Message))
		return
	}
	if users == nil {
		users = make([]xtls.InboundUser, 0)
	}

	write(w, http.StatusOK, envelope[struct {
		Users []xtls.InboundUser `json:"users"`
	}]{Response: struct {
		Users []xtls.InboundUser `json:"users"`
	}{Users: users}})
}

func (s *Service) HandleDropUsersConnections(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		UserIDs []string `json:"userIds"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	success := true
	if s.dropper != nil && s.provider != nil {
		success = s.dropper.DropUsers(r.Context(), s.provider, req.UserIDs)
	}

	write(w, http.StatusOK, envelope[struct {
		Success bool `json:"success"`
	}]{Response: struct {
		Success bool `json:"success"`
	}{Success: success}})
}

func (s *Service) HandleDropIPs(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if !decodeBody(r, &req) {
		writeError(write, w, "invalid JSON body")
		return
	}

	success := true
	if s.dropper != nil {
		success = s.dropper.DropIPs(req.IPs)
	}

	write(w, http.StatusOK, envelope[struct {
		Success bool `json:"success"`
	}]{Response: struct {
		Success bool `json:"success"`
	}{Success: success}})
}

func (s *Service) addSingleUser(ctx context.Context, item addUserItem) xtls.HandlerResult {
	switch item.Type {
	case "vless":
		return s.provider.HandlerAddVlessUser(ctx, item.Tag, item.Username, item.UUID, item.Flow, 0)
	case "trojan":
		return s.provider.HandlerAddTrojanUser(ctx, item.Tag, item.Username, item.Password, 0)
	case "shadowsocks":
		return s.provider.HandlerAddShadowsocksUser(ctx, item.Tag, item.Username, item.Password, item.CipherType, item.IVCheck, 0)
	case "shadowsocks22":
		return s.provider.HandlerAddShadowsocks2022User(ctx, item.Tag, item.Username, item.Password, 0)
	case "hysteria":
		return s.provider.HandlerAddHysteriaUser(ctx, item.Tag, item.Username, item.Password, 0)
	default:
		msg := "unsupported user type: " + item.Type
		return xtls.HandlerResult{OK: false, Message: msg}
	}
}

func (s *Service) addBatchUser(ctx context.Context, inbound batchInboundItem, user batchUserData) xtls.HandlerResult {
	switch inbound.Type {
	case "vless":
		return s.provider.HandlerAddVlessUser(ctx, inbound.Tag, user.UserID, user.VlessUUID, inbound.Flow, 0)
	case "trojan":
		return s.provider.HandlerAddTrojanUser(ctx, inbound.Tag, user.UserID, user.TrojanPassword, 0)
	case "shadowsocks":
		return s.provider.HandlerAddShadowsocksUser(ctx, inbound.Tag, user.UserID, user.SSPassword, 0, false, 0)
	case "shadowsocks22":
		key := base64.StdEncoding.EncodeToString([]byte(user.SSPassword))
		return s.provider.HandlerAddShadowsocks2022User(ctx, inbound.Tag, user.UserID, key, 0)
	case "hysteria":
		return s.provider.HandlerAddHysteriaUser(ctx, inbound.Tag, user.UserID, user.VlessUUID, 0)
	default:
		msg := "unsupported user type: " + inbound.Type
		return xtls.HandlerResult{OK: false, Message: msg}
	}
}

func collectUserIPs(ctx context.Context, provider Provider, username string) []string {
	if provider == nil || username == "" {
		return nil
	}
	entries, err := provider.GetUserIPList(ctx, username, true)
	if err != nil || len(entries) == 0 {
		return nil
	}
	ips := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IP != "" {
			ips = append(ips, entry.IP)
		}
	}
	return ips
}

func (s *Service) dropIPs(ips []string) {
	if s.dropper == nil || len(ips) == 0 {
		return
	}
	s.dropper.DropIPs(ips)
}

func aggregateResults(results []xtls.HandlerResult) genericResponse {
	if len(results) == 0 {
		return genericResponse{Success: true, Error: nil}
	}

	allFailed := true
	var firstError string
	for _, result := range results {
		if result.OK {
			allFailed = false
			continue
		}
		if firstError == "" && result.Message != "" {
			firstError = result.Message
		}
	}

	if allFailed {
		if firstError == "" {
			firstError = "all handler operations failed"
		}
		return genericResponse{Success: false, Error: stringPtr(firstError)}
	}
	return genericResponse{Success: true, Error: nil}
}

func decodeBody(r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return false
	}
	return true
}

func writeError(write writeJSONFn, w http.ResponseWriter, message string) {
	write(w, http.StatusBadRequest, map[string]any{"message": message})
}

func recoverHandler(write writeJSONFn, w http.ResponseWriter) {
	if recover() != nil {
		writeHandlerAPIError(write, w, errInternalServer, errInternalServer.Message)
	}
}

func handlerErrorMessage(resultMessage, fallback string) string {
	if resultMessage != "" {
		return resultMessage
	}
	return fallback
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

type tagRequest struct {
	Tag string `json:"tag"`
}

type addUserRequest struct {
	Data     []addUserItem `json:"data"`
	HashData hashData      `json:"hashData"`
}

type hashData struct {
	VlessUUID     string  `json:"vlessUuid"`
	PrevVlessUUID *string `json:"prevVlessUuid,omitempty"`
}

type addUserItem struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	UUID       string `json:"uuid"`
	Flow       string `json:"flow"`
	CipherType int    `json:"cipherType"`
	IVCheck    bool   `json:"ivCheck"`
}

type removeUserRequest struct {
	Username string   `json:"username"`
	HashData hashData `json:"hashData"`
}

type addUsersRequest struct {
	AffectedInboundTags []string    `json:"affectedInboundTags"`
	Users               []batchUser `json:"users"`
}

type batchUser struct {
	InboundData []batchInboundItem `json:"inboundData"`
	UserData    batchUserData      `json:"userData"`
}

type batchInboundItem struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
	Flow string `json:"flow"`
}

type batchUserData struct {
	UserID         string `json:"userId"`
	HashUUID       string `json:"hashUuid"`
	VlessUUID      string `json:"vlessUuid"`
	TrojanPassword string `json:"trojanPassword"`
	SSPassword     string `json:"ssPassword"`
}

type removeUsersRequest struct {
	Users []removeUsersItem `json:"users"`
}

type removeUsersItem struct {
	UserID   string `json:"userId"`
	HashUUID string `json:"hashUuid"`
}
