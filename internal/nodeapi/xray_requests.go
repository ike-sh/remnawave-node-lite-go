package nodeapi

import (
	"bytes"
	"encoding/json"
)

type XrayStartRequest struct {
	Internals  *XrayStartInternals `json:"internals"`
	XrayConfig *map[string]any     `json:"xrayConfig"`
}

type XrayStartInternals struct {
	ForceRestart OptionalBool    `json:"forceRestart"`
	Hashes       *XrayConfigHash `json:"hashes"`
}

type OptionalBool struct {
	Value   bool
	Present bool
	Null    bool
}

func (o *OptionalBool) UnmarshalJSON(raw []byte) error {
	o.Present = true
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		o.Null = true
		return nil
	}
	return json.Unmarshal(raw, &o.Value)
}

type XrayConfigHash struct {
	EmptyConfig *string            `json:"emptyConfig"`
	Inbounds    *[]XrayInboundHash `json:"inbounds"`
}

type XrayInboundHash struct {
	UsersCount *float64 `json:"usersCount"`
	Hash       *string  `json:"hash"`
	Tag        *string  `json:"tag"`
}

func (r *XrayStartRequest) Validate() []Issue {
	issues := make([]Issue, 0)
	if r.Internals == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"internals"}, "object"))
	} else {
		if r.Internals.ForceRestart.Present && r.Internals.ForceRestart.Null {
			issues = appendValidationIssues(issues, InvalidTypeIssue([]any{"internals", "forceRestart"}, "boolean", "null"))
		}
		if r.Internals.Hashes == nil {
			issues = appendValidationIssues(issues, MissingIssue([]any{"internals", "hashes"}, "object"))
		} else {
			issues = appendValidationIssues(issues, requireString(
				r.Internals.Hashes.EmptyConfig,
				[]any{"internals", "hashes", "emptyConfig"},
			)...)
			if r.Internals.Hashes.Inbounds == nil {
				issues = appendValidationIssues(issues, MissingIssue([]any{"internals", "hashes", "inbounds"}, "array"))
			} else {
				for index, inbound := range *r.Internals.Hashes.Inbounds {
					path := []any{"internals", "hashes", "inbounds", index}
					if inbound.UsersCount == nil {
						issues = appendValidationIssues(issues, MissingIssue(appendPath(path, "usersCount"), "number"))
					}
					issues = appendValidationIssues(issues, requireString(inbound.Hash, appendPath(path, "hash"))...)
					issues = appendValidationIssues(issues, requireString(inbound.Tag, appendPath(path, "tag"))...)
					if validationIssueLimitReached(issues) {
						return issues
					}
				}
			}
		}
	}
	if r.XrayConfig == nil {
		issues = appendValidationIssues(issues, MissingIssue([]any{"xrayConfig"}, "object"))
	}
	return issues
}
