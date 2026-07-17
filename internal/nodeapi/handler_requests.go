package nodeapi

import (
	"bytes"
	"encoding/json"
)

var handlerUserTypes = []any{"trojan", "vless", "shadowsocks", "shadowsocks22", "hysteria"}

type OptionalString struct {
	Value   string
	Present bool
	Null    bool
}

func (o *OptionalString) UnmarshalJSON(raw []byte) error {
	o.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		o.Null = true
		return nil
	}
	return json.Unmarshal(raw, &o.Value)
}

type AddUserRequest struct {
	Data     *[]AddUserItemRequest `json:"data"`
	HashData *AddUserHashRequest   `json:"hashData"`
}

type AddUserHashRequest struct {
	VlessUUID     *string        `json:"vlessUuid"`
	PrevVlessUUID OptionalString `json:"prevVlessUuid"`
}

type AddUserItemRequest struct {
	Type       *string `json:"type"`
	Tag        *string `json:"tag"`
	Username   *string `json:"username"`
	Password   *string `json:"password"`
	UUID       *string `json:"uuid"`
	Flow       *string `json:"flow"`
	CipherType *int    `json:"cipherType"`
	IVCheck    *bool   `json:"ivCheck"`
}

func (r *AddUserRequest) Validate() []Issue {
	issues := make([]Issue, 0)
	if r.Data == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"data"}, "array"))
	} else {
		for index := range *r.Data {
			issues = appendValidationIssues(issues, (*r.Data)[index].validate([]any{"data", index})...)
			if validationIssueLimitReached(issues) {
				return issues
			}
		}
	}
	if r.HashData == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"hashData"}, "object"))
	} else {
		issues = appendValidationIssues(issues, validateUUID(r.HashData.VlessUUID, []any{"hashData", "vlessUuid"})...)
		if r.HashData.PrevVlessUUID.Present {
			path := []any{"hashData", "prevVlessUuid"}
			if r.HashData.PrevVlessUUID.Null {
				issues = appendValidationIssues(issues, InvalidTypeIssue(path, "string", "null"))
			} else if !uuidPattern.MatchString(r.HashData.PrevVlessUUID.Value) {
				issues = appendValidationIssues(issues, invalidUUIDIssue(path))
			}
		}
	}
	return issues
}

func (r AddUserItemRequest) validate(path []any) []Issue {
	if r.Type == nil || !isHandlerUserType(*r.Type) {
		return []Issue{invalidDiscriminatorIssue(appendPath(path, "type"), handlerUserTypes...)}
	}

	issues := make([]Issue, 0, 6)
	issues = appendValidationIssues(issues, requireString(r.Tag, appendPath(path, "tag"))...)
	issues = appendValidationIssues(issues, requireString(r.Username, appendPath(path, "username"))...)
	switch *r.Type {
	case "trojan", "shadowsocks22", "hysteria":
		issues = appendValidationIssues(issues, requireString(r.Password, appendPath(path, "password"))...)
	case "vless":
		issues = appendValidationIssues(issues, requireString(r.UUID, appendPath(path, "uuid"))...)
		issues = appendValidationIssues(issues, validateFlow(r.Flow, appendPath(path, "flow"))...)
	case "shadowsocks":
		issues = appendValidationIssues(issues, requireString(r.Password, appendPath(path, "password"))...)
		issues = appendValidationIssues(issues, validateCipherType(r.CipherType, appendPath(path, "cipherType"))...)
		if r.IVCheck == nil {
			issues = appendValidationIssues(issues, MissingIssue(appendPath(path, "ivCheck"), "boolean"))
		}
	}
	return issues
}

type RemoveUserRequest struct {
	Username *string                `json:"username"`
	HashData *RemoveUserHashRequest `json:"hashData"`
}

type RemoveUserHashRequest struct {
	VlessUUID *string `json:"vlessUuid"`
}

func (r *RemoveUserRequest) Validate() []Issue {
	issues := requireString(r.Username, []any{"username"})
	if r.HashData == nil {
		return append(issues, MissingIssue([]any{"hashData"}, "object"))
	}
	return append(issues, validateUUID(r.HashData.VlessUUID, []any{"hashData", "vlessUuid"})...)
}

type AddUsersRequest struct {
	AffectedInboundTags *[]*string             `json:"affectedInboundTags"`
	Users               *[]AddUsersUserRequest `json:"users"`
}

type AddUsersUserRequest struct {
	InboundData *[]AddUsersInboundRequest `json:"inboundData"`
	UserData    *AddUsersUserDataRequest  `json:"userData"`
}

type AddUsersInboundRequest struct {
	Type *string `json:"type"`
	Tag  *string `json:"tag"`
	Flow *string `json:"flow"`
}

type AddUsersUserDataRequest struct {
	UserID         *string `json:"userId"`
	HashUUID       *string `json:"hashUuid"`
	VlessUUID      *string `json:"vlessUuid"`
	TrojanPassword *string `json:"trojanPassword"`
	SSPassword     *string `json:"ssPassword"`
}

func (r *AddUsersRequest) Validate() []Issue {
	issues := make([]Issue, 0)
	if r.AffectedInboundTags == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"affectedInboundTags"}, "array"))
	} else {
		for index, tag := range *r.AffectedInboundTags {
			issues = appendValidationIssues(issues, requireString(tag, []any{"affectedInboundTags", index})...)
			if validationIssueLimitReached(issues) {
				return issues
			}
		}
	}
	if r.Users == nil {
		return append(issues, MissingIssue([]any{"users"}, "array"))
	}
	for index := range *r.Users {
		issues = appendValidationIssues(issues, (*r.Users)[index].validate([]any{"users", index})...)
		if validationIssueLimitReached(issues) {
			break
		}
	}
	return issues
}

func (r AddUsersUserRequest) validate(path []any) []Issue {
	issues := make([]Issue, 0)
	if r.InboundData == nil {
		issues = appendValidationIssues(issues, MissingIssue(appendPath(path, "inboundData"), "array"))
	} else {
		for index := range *r.InboundData {
			issues = appendValidationIssues(issues, (*r.InboundData)[index].validate(appendPath(path, "inboundData", index))...)
			if validationIssueLimitReached(issues) {
				return issues
			}
		}
	}
	if r.UserData == nil {
		return append(issues, MissingIssue(appendPath(path, "userData"), "object"))
	}
	issues = appendValidationIssues(issues, requireString(r.UserData.UserID, appendPath(path, "userData", "userId"))...)
	issues = appendValidationIssues(issues, validateUUID(r.UserData.HashUUID, appendPath(path, "userData", "hashUuid"))...)
	issues = appendValidationIssues(issues, validateUUID(r.UserData.VlessUUID, appendPath(path, "userData", "vlessUuid"))...)
	issues = appendValidationIssues(issues, requireString(r.UserData.TrojanPassword, appendPath(path, "userData", "trojanPassword"))...)
	issues = appendValidationIssues(issues, requireString(r.UserData.SSPassword, appendPath(path, "userData", "ssPassword"))...)
	return issues
}

func (r AddUsersInboundRequest) validate(path []any) []Issue {
	if r.Type == nil || !isHandlerUserType(*r.Type) {
		return []Issue{invalidDiscriminatorIssue(appendPath(path, "type"), handlerUserTypes...)}
	}
	issues := requireString(r.Tag, appendPath(path, "tag"))
	if *r.Type == "vless" {
		issues = appendValidationIssues(issues, validateFlow(r.Flow, appendPath(path, "flow"))...)
	}
	return issues
}

type RemoveUsersRequest struct {
	Users *[]RemoveUsersItemRequest `json:"users"`
}

type RemoveUsersItemRequest struct {
	UserID   *string `json:"userId"`
	HashUUID *string `json:"hashUuid"`
}

func (r *RemoveUsersRequest) Validate() []Issue {
	if r.Users == nil {
		return []Issue{MissingIssue([]any{"users"}, "array")}
	}
	issues := make([]Issue, 0)
	for index, user := range *r.Users {
		issues = appendValidationIssues(issues, requireString(user.UserID, []any{"users", index, "userId"})...)
		issues = appendValidationIssues(issues, validateUUID(user.HashUUID, []any{"users", index, "hashUuid"})...)
		if validationIssueLimitReached(issues) {
			break
		}
	}
	return issues
}

type DropUsersConnectionsRequest struct {
	UserIDs *[]*string `json:"userIds"`
}

func (r *DropUsersConnectionsRequest) Validate() []Issue {
	if r.UserIDs == nil {
		return []Issue{MissingIssue([]any{"userIds"}, "array")}
	}
	if len(*r.UserIDs) == 0 {
		return []Issue{tooSmallArrayIssue([]any{"userIds"}, 1)}
	}
	issues := make([]Issue, 0)
	for index, userID := range *r.UserIDs {
		issues = appendValidationIssues(issues, requireString(userID, []any{"userIds", index})...)
		if validationIssueLimitReached(issues) {
			break
		}
	}
	return issues
}

type DropIPsRequest struct {
	IPs *[]*string `json:"ips"`
}

func (r *DropIPsRequest) Validate() []Issue {
	if r.IPs == nil {
		return []Issue{MissingIssue([]any{"ips"}, "array")}
	}
	if len(*r.IPs) == 0 {
		return []Issue{tooSmallArrayIssue([]any{"ips"}, 1)}
	}
	issues := make([]Issue, 0)
	for index, ip := range *r.IPs {
		issues = appendValidationIssues(issues, requireString(ip, []any{"ips", index})...)
		if validationIssueLimitReached(issues) {
			break
		}
	}
	return issues
}

func isHandlerUserType(value string) bool {
	for _, option := range handlerUserTypes {
		if value == option {
			return true
		}
	}
	return false
}

func validateFlow(value *string, path []any) []Issue {
	if value == nil {
		return []Issue{MissingIssue(path, "string")}
	}
	if *value != "xtls-rprx-vision" && *value != "" {
		return []Issue{invalidEnumIssue(path, *value, "xtls-rprx-vision", "")}
	}
	return nil
}

func validateCipherType(value *int, path []any) []Issue {
	if value == nil {
		return []Issue{MissingIssue(path, "number")}
	}
	allowed := []int{-1, 0, 5, 6, 7, 8, 9}
	for _, item := range allowed {
		if *value == item {
			return nil
		}
	}
	options := make([]any, len(allowed))
	for index, item := range allowed {
		options[index] = item
	}
	return []Issue{invalidEnumIssue(path, *value, options...)}
}
