package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strings"

	"remnawave-node-lite-go/internal/bodylimit"
)

// The Panel only consumes the HTTP status and the response envelope. Keep
// malformed/invalid requests on the official 400 error path without importing
// a Zod runtime or reproducing incidental framework error wording.
var uuidPattern = regexp.MustCompile(`(?i)^(?:[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}|00000000-0000-0000-0000-000000000000)$`)

func validatePostRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return true
	}
	original := r.Body
	if original == nil {
		original = http.NoBody
	}
	raw, err := io.ReadAll(io.LimitReader(original, bodylimit.MaxBytesLimit()+1))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"message": "request body too large", "error": "Payload Too Large", "statusCode": 413})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Invalid JSON body", "error": "Bad Request", "statusCode": 400})
		return false
	}
	if int64(len(raw)) > bodylimit.MaxBytesLimit() {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"message": "request body too large", "error": "Payload Too Large", "statusCode": 413})
		return false
	}
	_ = original.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	if !json.Valid(raw) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Invalid JSON body", "error": "Bad Request", "statusCode": 400})
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Invalid JSON body", "error": "Bad Request", "statusCode": 400})
		return false
	}
	if !requiresRequestSchema(r.URL.Path) {
		return true
	}
	var issues []map[string]any
	object, ok := value.(map[string]any)
	if !ok {
		issues = append(issues, typeIssue("object", value, nil, true))
	} else {
		issues = validateRouteBody(r.URL.Path, object)
	}
	if len(issues) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"statusCode": 400, "message": "Validation failed", "errors": issues})
		return false
	}
	return true
}

func requiresRequestSchema(path string) bool {
	switch path {
	case "/node/xray/start", "/node/stats/get-user-online-status", "/node/stats/get-users-stats",
		"/node/stats/get-inbound-stats", "/node/stats/get-outbound-stats", "/node/stats/get-all-inbounds-stats",
		"/node/stats/get-all-outbounds-stats", "/node/stats/get-combined-stats", "/node/stats/get-user-ip-list",
		"/node/stats/get-geocheck", "/node/handler/add-user", "/node/handler/remove-user",
		"/node/handler/add-users", "/node/handler/remove-users", "/node/handler/drop-users-connections",
		"/node/handler/drop-ips", "/node/plugin/sync", "/node/plugin/nftables/block-ips",
		"/node/plugin/nftables/unblock-ips":
		return true
	default:
		return false
	}
}

func validateRouteBody(path string, body map[string]any) []map[string]any {
	var issues []map[string]any
	check := func(obj map[string]any, key, expected string, parent ...any) any {
		item, exists := obj[key]
		location := append(append([]any{}, parent...), key)
		if !exists || !matches(item, expected) {
			issues = append(issues, typeIssue(expected, item, location, !exists))
		}
		return item
	}
	checkItems := func(items any, expected string, parent ...any) {
		array, ok := items.([]any)
		if !ok {
			return
		}
		for i, item := range array {
			location := append(append([]any{}, parent...), i)
			if !matches(item, expected) {
				issues = append(issues, typeIssue(expected, item, location, false))
			}
		}
	}
	switch path {
	case "/node/stats/get-users-stats", "/node/stats/get-all-inbounds-stats", "/node/stats/get-all-outbounds-stats", "/node/stats/get-combined-stats":
		check(body, "reset", "boolean")
	case "/node/stats/get-inbound-stats", "/node/stats/get-outbound-stats":
		check(body, "tag", "string")
		check(body, "reset", "boolean")
	case "/node/stats/get-user-online-status":
		check(body, "username", "string")
	case "/node/stats/get-user-ip-list":
		check(body, "userId", "string")
	case "/node/stats/get-geocheck":
		for _, key := range []string{"ip", "interface"} {
			if item, exists := body[key]; exists && !matches(item, "string") {
				issues = append(issues, typeIssue("string", item, []any{key}, false))
			}
		}
	case "/node/handler/remove-user":
		check(body, "username", "string")
		if hash, ok := check(body, "hashData", "object").(map[string]any); ok {
			check(hash, "vlessUuid", "uuid", "hashData")
		}
	case "/node/handler/add-user":
		if hash, ok := check(body, "hashData", "object").(map[string]any); ok {
			check(hash, "vlessUuid", "uuid", "hashData")
			if previous, exists := hash["prevVlessUuid"]; exists && !matches(previous, "uuid") {
				issues = append(issues, typeIssue("uuid", previous, []any{"hashData", "prevVlessUuid"}, false))
			}
		}
		if data, ok := check(body, "data", "array").([]any); ok {
			for i, raw := range data {
				user, valid := raw.(map[string]any)
				if !valid {
					issues = append(issues, typeIssue("object", raw, []any{"data", i}, false))
					continue
				}
				typ := check(user, "type", "string", "data", i)
				if !oneOf(typ, "trojan", "vless", "shadowsocks", "shadowsocks22", "hysteria") {
					if _, isString := typ.(string); isString {
						issues = append(issues, enumIssue(typ, []any{"data", i, "type"}))
					}
					continue
				}
				check(user, "tag", "string", "data", i)
				check(user, "username", "string", "data", i)
				switch typ {
				case "vless":
					check(user, "uuid", "string", "data", i)
					flow := check(user, "flow", "string", "data", i)
					if _, isString := flow.(string); isString && !oneOf(flow, "xtls-rprx-vision", "") {
						issues = append(issues, enumIssue(flow, []any{"data", i, "flow"}))
					}
				case "shadowsocks":
					check(user, "password", "string", "data", i)
					cipher := check(user, "cipherType", "number", "data", i)
					if number, ok := cipher.(float64); ok && !oneOfNumber(number, -1, 0, 5, 6, 7, 8, 9) {
						issues = append(issues, enumIssue(number, []any{"data", i, "cipherType"}))
					}
					check(user, "ivCheck", "boolean", "data", i)
				case "trojan", "shadowsocks22", "hysteria":
					check(user, "password", "string", "data", i)
				}
			}
		}
	case "/node/handler/add-users":
		affected := check(body, "affectedInboundTags", "array")
		checkItems(affected, "string", "affectedInboundTags")
		if users, ok := check(body, "users", "array").([]any); ok {
			for i, raw := range users {
				user, valid := raw.(map[string]any)
				if !valid {
					issues = append(issues, typeIssue("object", raw, []any{"users", i}, false))
					continue
				}
				if inbounds, ok := check(user, "inboundData", "array", "users", i).([]any); ok {
					for j, rawInbound := range inbounds {
						inbound, valid := rawInbound.(map[string]any)
						if !valid {
							issues = append(issues, typeIssue("object", rawInbound, []any{"users", i, "inboundData", j}, false))
							continue
						}
						typ := check(inbound, "type", "string", "users", i, "inboundData", j)
						if text, ok := typ.(string); ok && !oneOf(text, "trojan", "vless", "shadowsocks", "shadowsocks22", "hysteria") {
							issues = append(issues, enumIssue(text, []any{"users", i, "inboundData", j, "type"}))
						}
						check(inbound, "tag", "string", "users", i, "inboundData", j)
						if typ == "vless" {
							flow := check(inbound, "flow", "string", "users", i, "inboundData", j)
							if text, ok := flow.(string); ok && !oneOf(text, "xtls-rprx-vision", "") {
								issues = append(issues, enumIssue(text, []any{"users", i, "inboundData", j, "flow"}))
							}
						}
					}
				}
				if data, ok := check(user, "userData", "object", "users", i).(map[string]any); ok {
					for _, key := range []string{"userId", "trojanPassword", "ssPassword"} {
						check(data, key, "string", "users", i, "userData")
					}
					for _, key := range []string{"hashUuid", "vlessUuid"} {
						check(data, key, "uuid", "users", i, "userData")
					}
				}
			}
		}
	case "/node/handler/remove-users":
		if users, ok := check(body, "users", "array").([]any); ok {
			for i, raw := range users {
				user, valid := raw.(map[string]any)
				if !valid {
					issues = append(issues, typeIssue("object", raw, []any{"users", i}, false))
					continue
				}
				check(user, "userId", "string", "users", i)
				check(user, "hashUuid", "uuid", "users", i)
			}
		}
	case "/node/handler/drop-users-connections":
		items := check(body, "userIds", "array")
		checkItems(items, "string", "userIds")
		if array, ok := items.([]any); ok && len(array) == 0 {
			issues = append(issues, minIssue([]any{"userIds"}))
		}
	case "/node/handler/drop-ips":
		items := check(body, "ips", "array")
		checkItems(items, "string", "ips")
		if array, ok := items.([]any); ok && len(array) == 0 {
			issues = append(issues, minIssue([]any{"ips"}))
		}
	case "/node/plugin/sync":
		plugin, exists := body["plugin"]
		if !exists {
			issues = append(issues, typeIssue("object", nil, []any{"plugin"}, true))
			break
		}
		if plugin == nil {
			break
		}
		obj, ok := plugin.(map[string]any)
		if !ok {
			issues = append(issues, typeIssue("object", plugin, []any{"plugin"}, false))
			break
		}
		check(obj, "uuid", "uuid", "plugin")
		check(obj, "name", "string", "plugin")
		check(obj, "config", "record", "plugin")
	case "/node/plugin/nftables/block-ips":
		if items, ok := check(body, "ips", "array").([]any); ok {
			for i, raw := range items {
				obj, valid := raw.(map[string]any)
				if !valid {
					issues = append(issues, typeIssue("object", raw, []any{"ips", i}, false))
					continue
				}
				check(obj, "ip", "ip", "ips", i)
				check(obj, "timeout", "number", "ips", i)
			}
		}
	case "/node/plugin/nftables/unblock-ips":
		items := check(body, "ips", "array")
		checkItems(items, "ip", "ips")
	case "/node/xray/start":
		check(body, "xrayConfig", "object")
		if internals, ok := check(body, "internals", "object").(map[string]any); ok {
			if force, exists := internals["forceRestart"]; exists && !matches(force, "boolean") {
				issues = append(issues, typeIssue("boolean", force, []any{"internals", "forceRestart"}, false))
			}
			if integrations, exists := internals["integrations"]; exists && !matches(integrations, "record") {
				issues = append(issues, typeIssue("record", integrations, []any{"internals", "integrations"}, false))
			}
			if hashes, ok := check(internals, "hashes", "object", "internals").(map[string]any); ok {
				check(hashes, "emptyConfig", "string", "internals", "hashes")
				if inbounds, ok := check(hashes, "inbounds", "array", "internals", "hashes").([]any); ok {
					for i, raw := range inbounds {
						inbound, valid := raw.(map[string]any)
						if !valid {
							issues = append(issues, typeIssue("object", raw, []any{"internals", "hashes", "inbounds", i}, false))
							continue
						}
						check(inbound, "usersCount", "number", "internals", "hashes", "inbounds", i)
						check(inbound, "hash", "string", "internals", "hashes", "inbounds", i)
						check(inbound, "tag", "string", "internals", "hashes", "inbounds", i)
					}
				}
			}
		}
	}
	return issues
}

func matches(value any, expected string) bool {
	switch expected {
	case "object", "record":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "uuid":
		text, ok := value.(string)
		return ok && uuidPattern.MatchString(text)
	case "ip":
		text, ok := value.(string)
		if !ok {
			return false
		}
		_, err := netip.ParseAddr(text)
		return err == nil
	default:
		return false
	}
}

func oneOf(value any, choices ...string) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	for _, choice := range choices {
		if text == choice {
			return true
		}
	}
	return false
}

func oneOfNumber(value float64, choices ...float64) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func typeIssue(expected string, value any, path []any, missing bool) map[string]any {
	actual := "undefined"
	if !missing {
		actual = jsonType(value)
	}
	if path == nil {
		path = []any{}
	}
	if expected == "uuid" || expected == "ip" {
		return map[string]any{"code": "invalid_format", "format": expected, "path": path, "message": fmt.Sprintf("Invalid %s", expected)}
	}
	return map[string]any{"expected": expected, "code": "invalid_type", "path": path, "message": fmt.Sprintf("Invalid input: expected %s, received %s", expected, actual)}
}

func enumIssue(value any, path []any) map[string]any {
	return map[string]any{"code": "invalid_value", "path": path, "message": fmt.Sprintf("Invalid enum value %v", value)}
}

func minIssue(path []any) map[string]any {
	return map[string]any{"code": "too_small", "minimum": 1, "path": path, "message": "Array must contain at least one item"}
}

func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}
