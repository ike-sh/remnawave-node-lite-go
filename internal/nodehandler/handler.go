package nodehandler

import (
	"context"
	"encoding/base64"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/connections"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
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

type ConnectionDropper interface {
	DropIPs(ips []string) bool
	DropUsers(ctx context.Context, provider connections.IPListProvider, userIDs []string) bool
}

type Service struct {
	provider Provider
	dropper  ConnectionDropper
}

func NewService(provider Provider, dropper ConnectionDropper) *Service {
	return &Service{provider: provider, dropper: dropper}
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

	for _, item := range request.Data {
		s.provider.AddInboundTag(item.Tag)
	}

	hashUUID := request.HashData.VlessUUID
	if request.HashData.PrevVlessUUID != nil {
		hashUUID = *request.HashData.PrevVlessUUID
	}
	username := request.Data[0].Username
	for _, tag := range s.provider.InboundTags() {
		s.provider.HandlerRemoveUser(ctx, tag, username)
		s.provider.RemoveUserFromInboundHash(tag, hashUUID)
	}

	results := make([]xtls.HandlerResult, 0, len(request.Data))
	for _, item := range request.Data {
		result := s.addSingleUser(ctx, item)
		if result.OK {
			s.provider.AddUserToInboundHash(item.Tag, request.HashData.VlessUUID)
		}
		results = append(results, result)
	}
	return aggregateResults(results), nil
}

func (s *Service) RemoveUser(ctx context.Context, request RemoveUserRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}

	tags := s.provider.InboundTags()
	if len(tags) == 0 {
		return GenericResponse{Success: true, Error: nil}, nil
	}

	userIPs := collectUserIPs(ctx, s.provider, request.Username)
	results := make([]xtls.HandlerResult, 0, len(tags))
	for _, tag := range tags {
		results = append(results, s.provider.HandlerRemoveUser(ctx, tag, request.Username))
		s.provider.RemoveUserFromInboundHash(tag, request.VlessUUID)
	}
	s.dropIPs(userIPs)
	return aggregateResults(results), nil
}

func (s *Service) AddUsers(ctx context.Context, request AddUsersRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}

	for _, tag := range request.AffectedInboundTags {
		s.provider.AddInboundTag(tag)
	}
	for _, user := range request.Users {
		for _, tag := range s.provider.InboundTags() {
			s.provider.HandlerRemoveUser(ctx, tag, user.UserData.UserID)
			s.provider.RemoveUserFromInboundHash(tag, user.UserData.HashUUID)
		}
		for _, inbound := range user.InboundData {
			result := s.addBatchUser(ctx, inbound, user.UserData)
			if result.OK {
				s.provider.AddUserToInboundHash(inbound.Tag, user.UserData.VlessUUID)
			}
		}
	}

	// Official addUsers intentionally does not expose individual SDK failures.
	return GenericResponse{Success: true, Error: nil}, nil
}

func (s *Service) RemoveUsers(ctx context.Context, request RemoveUsersRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}

	tags := s.provider.InboundTags()
	if len(tags) == 0 {
		return GenericResponse{Success: true, Error: nil}, nil
	}

	results := make([]xtls.HandlerResult, 0, len(request.Users)*len(tags))
	for _, user := range request.Users {
		userIPs := collectUserIPs(ctx, s.provider, user.UserID)
		for _, tag := range tags {
			results = append(results, s.provider.HandlerRemoveUser(ctx, tag, user.UserID))
			s.provider.RemoveUserFromInboundHash(tag, user.HashUUID)
		}
		s.dropIPs(userIPs)
	}
	return aggregateResults(results), nil
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

func (s *Service) DropIPs(ips []string) SuccessResponse {
	success := true
	if s.dropper != nil {
		success = s.dropper.DropIPs(ips)
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

func (s *Service) dropIPs(ips []string) {
	if s.dropper != nil && len(ips) != 0 {
		s.dropper.DropIPs(ips)
	}
}

func aggregateResults(results []xtls.HandlerResult) GenericResponse {
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
		return GenericResponse{Success: false, Error: stringPtr(firstError)}
	}
	return GenericResponse{Success: true, Error: nil}
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
