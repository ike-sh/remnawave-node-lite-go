package nodehandler

import (
	"context"
	"encoding/base64"
	"log/slog"

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

type connectionDropperAvailability interface {
	Available() bool
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
	ctx = nonNilContext(ctx)
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
	var cleanup resultAccumulator
	for _, tag := range tags {
		if cleanup.StopForContext(ctx) {
			return cleanup.Response(), nil
		}
		result := s.provider.HandlerRemoveUser(ctx, tag, username)
		cleanup.Add(s.commitRemoved(result, tag, hashUUID))
	}
	if response := cleanup.Response(); !response.Success {
		return response, nil
	}

	var results resultAccumulator
	for _, item := range request.Data {
		if results.StopForContext(ctx) {
			return results.Response(), nil
		}
		result := s.addSingleUser(ctx, item)
		results.Add(s.commitAdded(result, item.Tag, request.HashData.VlessUUID))
	}
	return results.Response(), nil
}

func (s *Service) RemoveUser(ctx context.Context, request RemoveUserRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	ctx = nonNilContext(ctx)
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

	var results resultAccumulator
	dropPlan, lookupResult := s.prepareUserIPDrop(ctx, request.Username)
	results.Add(lookupResult)
	if !lookupResult.OK {
		return results.Response(), nil
	}
	removeOK := true
	for _, tag := range tags {
		if results.StopForContext(ctx) {
			return results.Response(), nil
		}
		result := s.provider.HandlerRemoveUser(ctx, tag, request.Username)
		result = s.commitRemoved(result, tag, request.VlessUUID)
		results.Add(result)
		removeOK = removeOK && result.OK
	}
	if !removeOK {
		return results.Response(), nil
	}
	results.Add(s.applyUserIPDrops(ctx, []userIPDropPlan{dropPlan}))
	return results.Response(), nil
}

func (s *Service) AddUsers(ctx context.Context, request AddUsersRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	ctx = nonNilContext(ctx)
	if s.provider == nil {
		return GenericResponse{}, errInternalServer
	}
	if !s.acquireMutation(ctx) {
		return GenericResponse{}, errInternalServer
	}
	defer s.releaseMutation()

	tags := batchMutationTags(s.provider.InboundTags(), request.AffectedInboundTags, request.Users)
	var results resultAccumulator
	for _, user := range request.Users {
		if results.StopForContext(ctx) {
			return results.Response(), nil
		}
		cleanupOK := true
		for _, tag := range tags {
			if results.StopForContext(ctx) {
				return results.Response(), nil
			}
			result := s.provider.HandlerRemoveUser(ctx, tag, user.UserData.UserID)
			result = s.commitRemoved(result, tag, user.UserData.HashUUID)
			results.Add(result)
			cleanupOK = cleanupOK && result.OK
		}
		if !cleanupOK {
			continue
		}
		for _, inbound := range user.InboundData {
			if results.StopForContext(ctx) {
				return results.Response(), nil
			}
			result := s.addBatchUser(ctx, inbound, user.UserData)
			results.Add(s.commitAdded(result, inbound.Tag, user.UserData.VlessUUID))
		}
	}
	return results.Response(), nil
}

func (s *Service) RemoveUsers(ctx context.Context, request RemoveUsersRequest) (response GenericResponse, err error) {
	defer recoverServiceError(&err)
	ctx = nonNilContext(ctx)
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

	var results resultAccumulator
	dropPlans := make([]userIPDropPlan, 0, len(request.Users))
	for _, user := range request.Users {
		if results.StopForContext(ctx) {
			return results.Response(), nil
		}
		dropPlan, lookupResult := s.prepareUserIPDrop(ctx, user.UserID)
		results.Add(lookupResult)
		if !lookupResult.OK {
			continue
		}
		removeOK := true
		for _, tag := range tags {
			if results.StopForContext(ctx) {
				return results.Response(), nil
			}
			result := s.provider.HandlerRemoveUser(ctx, tag, user.UserID)
			result = s.commitRemoved(result, tag, user.HashUUID)
			results.Add(result)
			removeOK = removeOK && result.OK
		}
		if !removeOK {
			continue
		}
		dropPlans = append(dropPlans, dropPlan)
	}
	results.Add(s.applyUserIPDrops(ctx, dropPlans))
	return results.Response(), nil
}

func (s *Service) GetInboundUsersCount(ctx context.Context, tag string) (InboundUsersCountResponse, error) {
	ctx = nonNilContext(ctx)
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
	ctx = nonNilContext(ctx)
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
	ctx = nonNilContext(ctx)
	success := true
	if s.dropper != nil && s.provider != nil {
		success = s.dropper.DropUsers(ctx, s.provider, userIDs)
	}
	return SuccessResponse{Success: success}
}

func (s *Service) DropIPs(ctx context.Context, ips []string) SuccessResponse {
	ctx = nonNilContext(ctx)
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

type userIPDropPlan struct {
	ips []string
}

func (s *Service) prepareUserIPDrop(ctx context.Context, userID string) (userIPDropPlan, xtls.HandlerResult) {
	if !s.connectionDropAvailable() || s.provider == nil {
		return userIPDropPlan{}, xtls.HandlerResult{OK: true}
	}
	// Removing a user must not clear retry evidence on reset-capable cores.
	entries, err := s.provider.GetUserIPList(ctx, userID, false)
	if err != nil {
		slog.Warn("failed to read user IP stats before removing user", "userId", userID, "error", err)
		return userIPDropPlan{}, xtls.HandlerResult{OK: false, Message: "failed to read user IP stats"}
	}
	plan := userIPDropPlan{}
	plan.ips = make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IP != "" {
			plan.ips = append(plan.ips, entry.IP)
		}
	}
	return plan, xtls.HandlerResult{OK: true}
}

func (s *Service) applyUserIPDrops(ctx context.Context, plans []userIPDropPlan) xtls.HandlerResult {
	allIPs := make([]string, 0)
	for _, plan := range plans {
		allIPs = append(allIPs, plan.ips...)
	}
	if len(allIPs) == 0 {
		return xtls.HandlerResult{OK: true}
	}
	if !s.dropper.DropIPs(ctx, allIPs) {
		return xtls.HandlerResult{OK: false, Message: "failed to drop user connections"}
	}
	return xtls.HandlerResult{OK: true}
}

func (s *Service) connectionDropAvailable() bool {
	if s.dropper == nil {
		return false
	}
	availability, ok := s.dropper.(connectionDropperAvailability)
	return !ok || availability.Available()
}

func requireAllResults(results []xtls.HandlerResult) GenericResponse {
	var accumulator resultAccumulator
	for _, result := range results {
		accumulator.Add(result)
	}
	return accumulator.Response()
}

type resultAccumulator struct {
	failed  bool
	message string
}

func (a *resultAccumulator) Add(result xtls.HandlerResult) {
	if !a.failed && !result.OK {
		a.failed = true
		a.message = result.Message
	}
}

func (a *resultAccumulator) StopForContext(ctx context.Context) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	a.Add(xtls.HandlerResult{OK: false, Message: ctx.Err().Error()})
	return true
}

func (a resultAccumulator) Response() GenericResponse {
	if a.failed {
		return GenericResponse{Success: false, Error: stringPtr(a.message)}
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
	ctx = nonNilContext(ctx)
	select {
	case s.mutationGate <- struct{}{}:
		if ctx.Err() != nil {
			s.releaseMutation()
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
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

func batchMutationTags(providerTags, affectedTags []string, users []BatchUser) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(providerTags)+len(affectedTags))
	appendTag := func(tag string) {
		if tag == "" {
			return
		}
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	for _, tag := range providerTags {
		appendTag(tag)
	}
	for _, tag := range affectedTags {
		appendTag(tag)
	}
	for _, user := range users {
		for _, inbound := range user.InboundData {
			appendTag(inbound.Tag)
		}
	}
	return result
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
