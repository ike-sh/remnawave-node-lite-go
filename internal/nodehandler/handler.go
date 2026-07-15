package nodehandler

import (
	"context"
	"encoding/base64"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

type Provider interface {
	InboundTags() []string
	CommitUserAdded(result xtls.HandlerResult, inboundTag, userUUID string) bool
	CommitUserRemoved(result xtls.HandlerResult, inboundTag, userUUID string) bool
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

type ConnectionDropper interface {
	DropIPs(ctx context.Context, ips []string) bool
	DropUsers(ctx context.Context, provider connections.IPListProvider, userIDs []string) bool
}

type Service struct {
	provider     Provider
	dropper      ConnectionDropper
	mutationGate chan struct{}
}

func NewService(provider Provider, dropper ConnectionDropper) *Service {
	return &Service{
		provider:     provider,
		dropper:      dropper,
		mutationGate: make(chan struct{}, 1),
	}
}

type GenericResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error"`
}

type SuccessResponse struct {
	Success bool `json:"success"`
}

type InboundUsersCountResponse struct {
	Count int64 `json:"count"`
}

type InboundUsersResponse struct {
	Users []xtls.InboundUser `json:"users"`
}

type AddUserRequest struct {
	Data     []AddUserItem
	HashData AddUserHashData
}

type AddUserHashData struct {
	VlessUUID     string
	PrevVlessUUID *string
}

type AddUserItem struct {
	Type       string
	Tag        string
	Username   string
	Password   string
	UUID       string
	Flow       string
	CipherType int
	IVCheck    bool
}

type RemoveUserRequest struct {
	Username  string
	VlessUUID string
}

type AddUsersRequest struct {
	AffectedInboundTags []string
	Users               []BatchUser
}

type BatchUser struct {
	InboundData []BatchInbound
	UserData    BatchUserData
}

type BatchInbound struct {
	Type string
	Tag  string
	Flow string
}

type BatchUserData struct {
	UserID         string
	HashUUID       string
	VlessUUID      string
	TrojanPassword string
	SSPassword     string
}

type RemoveUsersRequest struct {
	Users []RemoveUsersItem
}

type RemoveUsersItem struct {
	UserID   string
	HashUUID string
}

func (s *Service) AddUser(ctx context.Context, request AddUserRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}
	if len(request.Data) == 0 {
		return GenericResponse{Success: false, Error: nil}, nil
	}
	if !s.acquireMutation(ctx) {
		return GenericResponse{}, errInternalServer
	}
	defer s.releaseMutation()

	hashUUID := request.HashData.VlessUUID
	if request.HashData.PrevVlessUUID != nil {
		hashUUID = *request.HashData.PrevVlessUUID
	}
	username := request.Data[0].Username
	tags := userMutationTags(s.provider.InboundTags(), addUserTags(request.Data))
	cleanup := make([]xtls.HandlerResult, 0, len(tags))
	for _, tag := range tags {
		result := s.provider.HandlerRemoveUser(ctx, tag, username)
		cleanup = append(cleanup, s.commitRemoved(result, tag, hashUUID))
	}
	if response := requireAllResults(cleanup); !response.Success {
		return response, nil
	}

	results := make([]xtls.HandlerResult, 0, len(request.Data))
	for _, item := range request.Data {
		result := s.addSingleUser(ctx, item)
		results = append(results, s.commitAdded(result, item.Tag, request.HashData.VlessUUID))
	}
	return requireAllResults(results), nil
}

func (s *Service) RemoveUser(ctx context.Context, request RemoveUserRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}
	if !s.acquireMutation(ctx) {
		return GenericResponse{}, errInternalServer
	}
	defer s.releaseMutation()

	tags := s.provider.InboundTags()
	if len(tags) == 0 {
		return GenericResponse{Success: true, Error: nil}, nil
	}

	userIPs := collectUserIPs(ctx, s.provider, request.Username)
	results := make([]xtls.HandlerResult, 0, len(tags))
	for _, tag := range tags {
		result := s.provider.HandlerRemoveUser(ctx, tag, request.Username)
		results = append(results, s.commitRemoved(result, tag, request.VlessUUID))
	}
	s.dropIPs(ctx, userIPs)
	return requireAllResults(results), nil
}

func (s *Service) AddUsers(ctx context.Context, request AddUsersRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}
	if !s.acquireMutation(ctx) {
		return GenericResponse{}, errInternalServer
	}
	defer s.releaseMutation()

	tags := userMutationTags(s.provider.InboundTags(), request.AffectedInboundTags, batchInboundTags(request.Users))
	results := make([]xtls.HandlerResult, 0)
	for _, user := range request.Users {
		cleanupOK := true
		for _, tag := range tags {
			result := s.provider.HandlerRemoveUser(ctx, tag, user.UserData.UserID)
			result = s.commitRemoved(result, tag, user.UserData.HashUUID)
			results = append(results, result)
			cleanupOK = cleanupOK && result.OK
		}
		if !cleanupOK {
			continue
		}
		for _, inbound := range user.InboundData {
			result := s.addBatchUser(ctx, inbound, user.UserData)
			results = append(results, s.commitAdded(result, inbound.Tag, user.UserData.VlessUUID))
		}
	}
	return requireAllResults(results), nil
}

func (s *Service) RemoveUsers(ctx context.Context, request RemoveUsersRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}
	if !s.acquireMutation(ctx) {
		return GenericResponse{}, errInternalServer
	}
	defer s.releaseMutation()

	tags := s.provider.InboundTags()
	if len(tags) == 0 {
		return GenericResponse{Success: true, Error: nil}, nil
	}

	results := make([]xtls.HandlerResult, 0, len(request.Users)*len(tags))
	for _, user := range request.Users {
		userIPs := collectUserIPs(ctx, s.provider, user.UserID)
		for _, tag := range tags {
			result := s.provider.HandlerRemoveUser(ctx, tag, user.UserID)
			results = append(results, s.commitRemoved(result, tag, user.HashUUID))
		}
		s.dropIPs(ctx, userIPs)
	}
	return requireAllResults(results), nil
}

func (s *Service) GetInboundUsersCount(ctx context.Context, tag string) (InboundUsersCountResponse, error) {
	if s.provider == nil {
		return InboundUsersCountResponse{Count: 0}, nil
	}
	count, result := s.provider.HandlerGetInboundUsersCount(ctx, tag)
	if !result.OK {
		return InboundUsersCountResponse{}, errFailedInboundUsers
	}
	return InboundUsersCountResponse{Count: count}, nil
}

func (s *Service) GetInboundUsers(ctx context.Context, tag string) (InboundUsersResponse, error) {
	if s.provider == nil {
		return InboundUsersResponse{Users: []xtls.InboundUser{}}, nil
	}
	users, result := s.provider.HandlerGetInboundUsers(ctx, tag)
	if !result.OK {
		return InboundUsersResponse{}, errFailedInboundUsers
	}
	if users == nil {
		users = []xtls.InboundUser{}
	}
	return InboundUsersResponse{Users: users}, nil
}

func (s *Service) DropUsersConnections(ctx context.Context, userIDs []string) SuccessResponse {
	success := true
	if s.dropper != nil && s.provider != nil {
		success = s.dropper.DropUsers(ctx, s.provider, userIDs)
	}
	return SuccessResponse{Success: success}
}

func (s *Service) DropIPs(ctx context.Context, ips []string) SuccessResponse {
	success := true
	if s.dropper != nil {
		success = s.dropper.DropIPs(ctx, ips)
	}
	return SuccessResponse{Success: success}
}

func (s *Service) addSingleUser(ctx context.Context, item AddUserItem) xtls.HandlerResult {
	switch item.Type {
	case "vless":
		return s.provider.HandlerAddVlessUser(ctx, item.Tag, item.Username, item.UUID, item.Flow, 0)
	case "trojan":
		return s.provider.HandlerAddTrojanUser(ctx, item.Tag, item.Username, item.Password, 0)
	case "shadowsocks":
		return s.provider.HandlerAddShadowsocksUser(ctx, item.Tag, item.Username, item.Password, item.CipherType, false, 0)
	case "shadowsocks22":
		return s.provider.HandlerAddShadowsocks2022User(ctx, item.Tag, item.Username, item.Password, 0)
	case "hysteria":
		return s.provider.HandlerAddHysteriaUser(ctx, item.Tag, item.Username, item.Password, 0)
	default:
		return xtls.HandlerResult{OK: false, Message: "unsupported user type: " + item.Type}
	}
}

func (s *Service) addBatchUser(ctx context.Context, inbound BatchInbound, user BatchUserData) xtls.HandlerResult {
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
		return xtls.HandlerResult{OK: false, Message: "unsupported user type: " + inbound.Type}
	}
}

func collectUserIPs(ctx context.Context, provider Provider, username string) []string {
	if provider == nil {
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

func (s *Service) dropIPs(ctx context.Context, ips []string) {
	if s.dropper != nil && len(ips) != 0 {
		s.dropper.DropIPs(ctx, ips)
	}
}

func requireAllResults(results []xtls.HandlerResult) GenericResponse {
	for _, result := range results {
		if !result.OK {
			return GenericResponse{Success: false, Error: stringPtr(result.Message)}
		}
	}
	return GenericResponse{Success: true, Error: nil}
}

func (s *Service) commitAdded(result xtls.HandlerResult, tag, hashUUID string) xtls.HandlerResult {
	if result.OK && !s.provider.CommitUserAdded(result, tag, hashUUID) {
		return xtls.HandlerResult{OK: false, Message: "Xray lifecycle changed before user state commit"}
	}
	return result
}

func (s *Service) commitRemoved(result xtls.HandlerResult, tag, hashUUID string) xtls.HandlerResult {
	if result.OK && !s.provider.CommitUserRemoved(result, tag, hashUUID) {
		return xtls.HandlerResult{OK: false, Message: "Xray lifecycle changed before user state commit"}
	}
	return result
}

func (s *Service) acquireMutation(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.mutationGate <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Service) releaseMutation() {
	<-s.mutationGate
}

func userMutationTags(groups ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, group := range groups {
		for _, tag := range group {
			if tag == "" {
				continue
			}
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			result = append(result, tag)
		}
	}
	return result
}

func addUserTags(items []AddUserItem) []string {
	tags := make([]string, 0, len(items))
	for _, item := range items {
		tags = append(tags, item.Tag)
	}
	return tags
}

func batchInboundTags(users []BatchUser) []string {
	var tags []string
	for _, user := range users {
		for _, inbound := range user.InboundData {
			tags = append(tags, inbound.Tag)
		}
	}
	return tags
}

func recoverServiceError(err *error) {
	if recover() != nil {
		*err = errInternalServer
	}
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
