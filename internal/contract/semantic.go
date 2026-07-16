package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

func successSemanticHash(routeID string, raw []byte) (string, error) {
	root, err := decodeSemanticObject(raw)
	if err != nil {
		return "", err
	}
	response, ok := root["response"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("response envelope is not an object")
	}

	var projection any
	switch routeID {
	case "xray.start":
		info, _ := response["nodeInformation"].(map[string]any)
		projection = map[string]any{
			"isStarted":  response["isStarted"],
			"version":    response["version"],
			"errorIsNil": response["error"] == nil,
			"nodeVersion": func() any {
				if info == nil {
					return nil
				}
				return info["version"]
			}(),
		}
	case "xray.stop":
		projection = selectSemanticFields(response, "isStopped")
	case "xray.healthcheck":
		projection = selectSemanticFields(response, "isAlive", "xrayInternalStatusCached", "xrayVersion", "nodeVersion")
	case "stats.user-online-status":
		projection = selectSemanticFields(response, "isOnline")
	case "stats.users":
		projection, err = identityArrayProjection(response, "users", "username")
	case "stats.system":
		projection = map[string]any{"xrayInfoPresent": response["xrayInfo"] != nil}
	case "stats.inbound":
		projection = selectSemanticFields(response, "inbound")
	case "stats.outbound":
		projection = selectSemanticFields(response, "outbound")
	case "stats.all-inbounds":
		projection, err = identityArrayProjection(response, "inbounds", "inbound")
	case "stats.all-outbounds":
		projection, err = identityArrayProjection(response, "outbounds", "outbound")
	case "stats.combined":
		var inbounds, outbounds any
		inbounds, err = identityArrayProjection(response, "inbounds", "inbound")
		if err == nil {
			outbounds, err = identityArrayProjection(response, "outbounds", "outbound")
		}
		projection = map[string]any{"inbounds": inbounds, "outbounds": outbounds}
	case "stats.user-ip-list":
		projection, err = identityArrayProjection(response, "ips", "ip")
	case "stats.users-ip-list":
		projection, err = usersIPProjection(response)
	case "handler.add-user", "handler.remove-user", "handler.add-users", "handler.remove-users":
		projection = map[string]any{"success": response["success"], "errorIsNil": response["error"] == nil}
	case "handler.inbound-users-count":
		projection = selectSemanticFields(response, "count")
	case "handler.inbound-users":
		projection, err = identityArrayProjection(response, "users", "username")
	case "handler.drop-users-connections", "handler.drop-ips":
		projection = selectSemanticFields(response, "success")
	case "plugin.sync", "plugin.nftables.block-ips", "plugin.nftables.unblock-ips", "plugin.nftables.recreate-tables":
		projection = selectSemanticFields(response, "accepted")
	case "plugin.torrent-blocker.collect":
		projection, err = torrentReportsProjection(response)
	default:
		return "", fmt.Errorf("unsupported route %q", routeID)
	}
	if err != nil {
		return "", err
	}
	return semanticHash(projection)
}

func errorSemanticHash(kind string, raw []byte) (string, error) {
	root, err := decodeSemanticObject(raw)
	if err != nil {
		return "", err
	}
	var projection map[string]any
	switch kind {
	case "application":
		projection = selectSemanticFields(root, "path", "message", "errorCode")
	case "generic":
		projection = selectSemanticFields(root, "statusCode", "message", "error")
	default:
		return "", fmt.Errorf("unsupported error kind %q", kind)
	}
	return semanticHash(projection)
}

func decodeSemanticObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func selectSemanticFields(object map[string]any, fields ...string) map[string]any {
	selected := make(map[string]any, len(fields))
	for _, field := range fields {
		selected[field] = object[field]
	}
	return selected
}

func identityArrayProjection(object map[string]any, arrayField string, identityFields ...string) (any, error) {
	items, ok := object[arrayField].([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", arrayField)
	}
	identities := make([]string, 0, len(items))
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d] is not an object", arrayField, index)
		}
		encoded, err := json.Marshal(selectSemanticFields(item, identityFields...))
		if err != nil {
			return nil, err
		}
		identities = append(identities, string(encoded))
	}
	sort.Strings(identities)
	return identities, nil
}

func usersIPProjection(response map[string]any) (any, error) {
	users, ok := response["users"].([]any)
	if !ok {
		return nil, fmt.Errorf("users is not an array")
	}
	identities := make([]string, 0, len(users))
	for index, rawUser := range users {
		user, ok := rawUser.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("users[%d] is not an object", index)
		}
		ips, err := identityArrayProjection(user, "ips", "ip")
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(map[string]any{"userId": user["userId"], "ips": ips})
		if err != nil {
			return nil, err
		}
		identities = append(identities, string(encoded))
	}
	sort.Strings(identities)
	return identities, nil
}

func torrentReportsProjection(response map[string]any) (any, error) {
	reports, ok := response["reports"].([]any)
	if !ok {
		return nil, fmt.Errorf("reports is not an array")
	}
	identities := make([]string, 0, len(reports))
	for index, rawReport := range reports {
		report, ok := rawReport.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("reports[%d] is not an object", index)
		}
		action, _ := report["actionReport"].(map[string]any)
		xray, _ := report["xrayReport"].(map[string]any)
		encoded, err := json.Marshal(map[string]any{
			"action": selectSemanticFields(action, "blocked", "ip", "blockDuration", "userId"),
			"xray":   selectSemanticFields(xray, "email", "protocol", "network", "source", "destination", "inboundTag", "outboundTag"),
		})
		if err != nil {
			return nil, err
		}
		identities = append(identities, string(encoded))
	}
	sort.Strings(identities)
	return identities, nil
}

func semanticHash(value any) (string, error) {
	normalized, err := normalizeSemanticValue(value)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeSemanticValue(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		number, ok := new(big.Rat).SetString(string(typed))
		if !ok {
			return nil, fmt.Errorf("invalid JSON number %q", typed)
		}
		return number.RatString(), nil
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, item := range typed {
			value, err := normalizeSemanticValue(item)
			if err != nil {
				return nil, err
			}
			normalized[key] = value
		}
		return normalized, nil
	case []any:
		normalized := make([]any, len(typed))
		for index, item := range typed {
			value, err := normalizeSemanticValue(item)
			if err != nil {
				return nil, err
			}
			normalized[index] = value
		}
		return normalized, nil
	default:
		return value, nil
	}
}
