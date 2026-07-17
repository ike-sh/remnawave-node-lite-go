package nodeapi

import (
	"bytes"
	"encoding/json"
)

type PluginSyncRequest struct {
	Plugin NullablePluginRequest `json:"plugin"`
}

type NullablePluginRequest struct {
	Value   PluginRequest
	Present bool
	Null    bool
}

func (*NullablePluginRequest) structuralJSONSchema() any {
	return PluginRequest{}
}

func (p *NullablePluginRequest) UnmarshalJSON(raw []byte) error {
	p.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		p.Null = true
		return nil
	}
	return json.Unmarshal(raw, &p.Value)
}

type PluginRequest struct {
	Config *json.RawMessage `json:"config"`
	UUID   *string          `json:"uuid"`
	Name   *string          `json:"name"`
}

func (r *PluginSyncRequest) Validate() []Issue {
	if !r.Plugin.Present {
		return []Issue{MissingIssue([]any{"plugin"}, "object")}
	}
	if r.Plugin.Null {
		return nil
	}

	issues := make([]Issue, 0, 3)
	configPath := []any{"plugin", "config"}
	if r.Plugin.Value.Config == nil {
		issues = appendValidationIssues(issues, MissingIssue(configPath, "object"))
	} else if !rawJSONObject(*r.Plugin.Value.Config) {
		issues = appendValidationIssues(issues, InvalidTypeIssue(configPath, "object", rawJSONType(*r.Plugin.Value.Config)))
	}
	issues = appendValidationIssues(issues, validateUUID(r.Plugin.Value.UUID, []any{"plugin", "uuid"})...)
	issues = appendValidationIssues(issues, requireString(r.Plugin.Value.Name, []any{"plugin", "name"})...)
	return issues
}

type BlockIPsRequest struct {
	IPs *[]BlockIPRequest `json:"ips"`
}

type BlockIPRequest struct {
	IP      *string  `json:"ip"`
	Timeout *float64 `json:"timeout"`
}

func (r *BlockIPsRequest) Validate() []Issue {
	if r.IPs == nil {
		return []Issue{MissingIssue([]any{"ips"}, "array")}
	}
	issues := make([]Issue, 0)
	for index, item := range *r.IPs {
		issues = appendValidationIssues(issues, validateIP(item.IP, []any{"ips", index, "ip"})...)
		if item.Timeout == nil {
			issues = appendValidationIssues(issues, MissingIssue([]any{"ips", index, "timeout"}, "number"))
		}
		if validationIssueLimitReached(issues) {
			break
		}
	}
	return issues
}

type UnblockIPsRequest struct {
	IPs *[]string `json:"ips"`
}

func (r *UnblockIPsRequest) Validate() []Issue {
	if r.IPs == nil {
		return []Issue{MissingIssue([]any{"ips"}, "array")}
	}
	issues := make([]Issue, 0)
	for index := range *r.IPs {
		value := &(*r.IPs)[index]
		issues = appendValidationIssues(issues, validateIP(value, []any{"ips", index})...)
		if validationIssueLimitReached(issues) {
			break
		}
	}
	return issues
}

func rawJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func rawJSONType(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "undefined"
	}
	switch trimmed[0] {
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
