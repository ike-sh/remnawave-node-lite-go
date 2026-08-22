package plugin

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"strings"
)

// Validation aligned with @remnawave/node-plugins@0.7.3 (NodePluginSchema).

func isPlainIP(value string) bool {
	return net.ParseIP(value) != nil
}

func isIPv4CIDR(value string) bool {
	ip, _, err := net.ParseCIDR(value)
	if err != nil {
		return false
	}
	return ip.To4() != nil && strings.Contains(value, ".")
}

func isIPv6CIDR(value string) bool {
	ip, _, err := net.ParseCIDR(value)
	if err != nil {
		return false
	}
	return ip.To4() == nil && strings.Contains(value, ":")
}

func isIPCidrOrExt(value string) bool {
	if strings.HasPrefix(value, "ext:") {
		return true
	}
	return isPlainIP(value) || isIPv4CIDR(value) || isIPv6CIDR(value)
}

func isIPOrExt(value string) bool {
	if strings.HasPrefix(value, "ext:") {
		return true
	}
	return isPlainIP(value)
}

func isSharedListItem(value string) bool {
	return isPlainIP(value) || isIPv4CIDR(value) || isIPv6CIDR(value)
}

func validateStringArray(field string, raw any, itemCheck func(string) bool) error {
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", field)
	}
	for i, item := range items {
		value, ok := item.(string)
		if !ok {
			return fmt.Errorf("%s[%d] must be a string", field, i)
		}
		if !itemCheck(value) {
			return fmt.Errorf("%s[%d] has invalid value %q", field, i, value)
		}
	}
	return nil
}

func validateASNArray(field string, raw any) error {
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", field)
	}
	for i, item := range items {
		asn, ok := asInt(item)
		if !ok || asn < 1 || uint64(asn) > uint64(^uint32(0)) {
			return fmt.Errorf("%s[%d] must be a positive AS number", field, i)
		}
	}
	return nil
}

func validateSharedLists(raw any) error {
	if raw == nil {
		return nil
	}
	lists, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("sharedLists must be an array")
	}
	for i, entry := range lists {
		obj, ok := entry.(map[string]any)
		if !ok {
			return fmt.Errorf("sharedLists[%d] must be an object", i)
		}
		name, _ := obj["name"].(string)
		if !strings.HasPrefix(name, "ext:") {
			return fmt.Errorf("sharedLists[%d].name must start with %q", i, "ext:")
		}
		switch listType, _ := obj["type"].(string); listType {
		case "ipList":
			if err := validateStringArray(fmt.Sprintf("sharedLists[%d].items", i), obj["items"], isSharedListItem); err != nil {
				return err
			}
		case "asList":
			if err := validateASNArray(fmt.Sprintf("sharedLists[%d].items", i), obj["items"]); err != nil {
				return err
			}
		default:
			return fmt.Errorf("sharedLists[%d].type must be ipList or asList", i)
		}
	}
	return nil
}

func validateTorrentBlockerSection(raw any) error {
	if raw == nil {
		return nil
	}
	section, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("torrentBlocker must be an object")
	}
	if _, ok := section["enabled"].(bool); !ok {
		return fmt.Errorf("torrentBlocker.enabled is required and must be a boolean")
	}
	if !isIntNumber(section["blockDuration"]) {
		return fmt.Errorf("torrentBlocker.blockDuration is required and must be an integer")
	}
	ignoreRaw, ok := section["ignoreLists"]
	if !ok {
		return fmt.Errorf("torrentBlocker.ignoreLists is required")
	}
	ignore, ok := ignoreRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("torrentBlocker.ignoreLists must be an object")
	}
	if ips, ok := ignore["ip"]; ok {
		if err := validateStringArray("torrentBlocker.ignoreLists.ip", ips, isIPOrExt); err != nil {
			return err
		}
	}
	if users, ok := ignore["userId"]; ok {
		if err := validateIntArray("torrentBlocker.ignoreLists.userId", users); err != nil {
			return err
		}
	}
	if tags, ok := section["includeRuleTags"]; ok {
		items, ok := tags.([]any)
		if !ok {
			return fmt.Errorf("torrentBlocker.includeRuleTags must be an array")
		}
		if len(items) < 1 {
			return fmt.Errorf("torrentBlocker.includeRuleTags must contain at least one item")
		}
		for i, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("torrentBlocker.includeRuleTags[%d] must be a string", i)
			}
		}
	}
	if webhook, ok := section["webhookUrl"]; ok {
		value, ok := webhook.(string)
		if !ok {
			return fmt.Errorf("torrentBlocker.webhookUrl must be a string")
		}
		parsed, err := url.Parse(value)
		if err != nil || !parsed.IsAbs() || (parsed.Host == "" && parsed.Opaque == "") {
			return fmt.Errorf("torrentBlocker.webhookUrl must be an absolute URL")
		}
	}
	if placement, ok := section["rulePlacement"]; ok {
		value, valid := asNumber(placement)
		if !valid || value < 0 || value > 1000 {
			return fmt.Errorf("torrentBlocker.rulePlacement must be a number between 0 and 1000")
		}
	}
	return nil
}

func validatePreStartSection(raw any) error {
	if raw == nil {
		return nil
	}
	section, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("preStart must be an object")
	}
	if enabled, exists := section["enabled"]; exists {
		if _, ok := enabled.(bool); !ok {
			return fmt.Errorf("preStart.enabled must be a boolean")
		}
	}
	cleanupRaw, exists := section["cleanupSockets"]
	if !exists || cleanupRaw == nil {
		return nil
	}
	cleanup, ok := cleanupRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("preStart.cleanupSockets must be an object")
	}
	if _, ok := cleanup["enabled"].(bool); !ok {
		return fmt.Errorf("preStart.cleanupSockets.enabled is required and must be a boolean")
	}
	files, ok := cleanup["files"].([]any)
	if !ok {
		return fmt.Errorf("preStart.cleanupSockets.files must be an array")
	}
	if len(files) > 64 {
		return fmt.Errorf("preStart.cleanupSockets.files must contain no more than 64 entries")
	}
	for index, item := range files {
		path, ok := item.(string)
		if !ok {
			return fmt.Errorf("preStart.cleanupSockets.files[%d] must be a string", index)
		}
		path = strings.TrimSpace(path)
		if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsRune(path, '\x00') {
			return fmt.Errorf("preStart.cleanupSockets.files[%d] must be a non-empty absolute path without null bytes", index)
		}
	}
	return nil
}

func validateConnectionDropSection(raw any) error {
	if raw == nil {
		return nil
	}
	section, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("connectionDrop must be an object")
	}
	if _, ok := section["enabled"].(bool); !ok {
		return fmt.Errorf("connectionDrop.enabled is required and must be a boolean")
	}
	if err := validateStringArray("connectionDrop.whitelistIps", section["whitelistIps"], isIPOrExt); err != nil {
		return err
	}
	return nil
}

func validateIngressFilterSection(raw any) error {
	if raw == nil {
		return nil
	}
	section, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("ingressFilter must be an object")
	}
	if _, ok := section["enabled"].(bool); !ok {
		return fmt.Errorf("ingressFilter.enabled is required and must be a boolean")
	}
	if err := validateStringArray("ingressFilter.blockedIps", section["blockedIps"], isIPCidrOrExt); err != nil {
		return err
	}
	return nil
}

func validateEgressFilterSection(raw any) error {
	if raw == nil {
		return nil
	}
	section, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("egressFilter must be an object")
	}
	if _, ok := section["enabled"].(bool); !ok {
		return fmt.Errorf("egressFilter.enabled is required and must be a boolean")
	}
	if ips, ok := section["blockedIps"]; ok {
		if err := validateStringArray("egressFilter.blockedIps", ips, isIPCidrOrExt); err != nil {
			return err
		}
	}
	if ports, ok := section["blockedPorts"]; ok {
		if err := validatePortArray("egressFilter.blockedPorts", ports); err != nil {
			return err
		}
	}
	return nil
}

func isIntNumber(value any) bool {
	_, ok := asInt(value)
	return ok
}

func validateIntArray(field string, raw any) error {
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", field)
	}
	for i, item := range items {
		if !isIntNumber(item) {
			return fmt.Errorf("%s[%d] must be an integer", field, i)
		}
	}
	return nil
}

func validatePortArray(field string, raw any) error {
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", field)
	}
	for i, item := range items {
		port, ok := asInt(item)
		if !ok || port < 1 || port > 65535 {
			return fmt.Errorf("%s[%d] must be an integer between 1 and 65535", field, i)
		}
	}
	return nil
}

func asInt(value any) (int, bool) {
	const maxSafeInteger = int64(1<<53 - 1)
	toInt := func(v int64) (int, bool) {
		if v < -maxSafeInteger || v > maxSafeInteger {
			return 0, false
		}
		converted := int(v)
		return converted, int64(converted) == v
	}
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v ||
			v < -float64(maxSafeInteger) || v > float64(maxSafeInteger) {
			return 0, false
		}
		return toInt(int64(v))
	case int:
		return toInt(int64(v))
	case int64:
		return toInt(v)
	default:
		return 0, false
	}
}

func asNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, false
		}
		return typed, true
	case float32:
		value := float64(typed)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, false
		}
		return value, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}
