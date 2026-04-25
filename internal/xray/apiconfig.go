package xray

import "strings"

const (
	apiTag        = "REMNAWAVE_API"
	apiInboundTag = "REMNAWAVE_API_INBOUND"
)

func generateAPIConfig(input map[string]any, xtlsAPIPort int, certs internalCerts) map[string]any {
	result := cloneMap(input)

	result["stats"] = map[string]any{}
	result["api"] = map[string]any{
		"services": []any{"HandlerService", "StatsService", "RoutingService"},
		"tag":      apiTag,
	}
	result["inbounds"] = append(
		[]any{apiInbound(xtlsAPIPort, certs)},
		arrayFrom(result["inbounds"])...,
	)
	result["outbounds"] = arrayFrom(result["outbounds"])
	result["policy"] = policyFrom(result["policy"])
	result["routing"] = routingFrom(result["routing"])

	return result
}

func apiInbound(port int, certs internalCerts) map[string]any {
	return map[string]any{
		"tag":      apiInboundTag,
		"port":     port,
		"listen":   "127.0.0.1",
		"protocol": "dokodemo-door",
		"settings": map[string]any{
			"address": "127.0.0.1",
		},
		"streamSettings": map[string]any{
			"security": "tls",
			"tlsSettings": map[string]any{
				"alpn":              []any{"h2"},
				"serverName":        "internal.remnawave.local",
				"disableSystemRoot": true,
				"rejectUnknownSni":  true,
				"certificates": []any{
					map[string]any{
						"certificate": pemLines(certs.ServerCertPEM),
						"key":         pemLines(certs.ServerKeyPEM),
					},
					map[string]any{
						"usage":       "verify",
						"certificate": pemLines(certs.CACertPEM),
					},
				},
			},
		},
	}
}

func policyFrom(existing any) map[string]any {
	levelZero := map[string]any{}
	if existingPolicy, ok := existing.(map[string]any); ok {
		if levels, ok := existingPolicy["levels"].(map[string]any); ok {
			if zero, ok := levels["0"].(map[string]any); ok {
				for key, value := range zero {
					levelZero[key] = value
				}
			}
		}
	}

	levelZero["statsUserUplink"] = true
	levelZero["statsUserDownlink"] = true
	levelZero["statsUserOnline"] = true

	return map[string]any{
		"levels": map[string]any{
			"0": levelZero,
		},
		"system": map[string]any{
			"statsInboundDownlink":  true,
			"statsInboundUplink":    true,
			"statsOutboundDownlink": true,
			"statsOutboundUplink":   true,
		},
	}
}

func routingFrom(existing any) map[string]any {
	routing := map[string]any{}
	if existingRouting, ok := existing.(map[string]any); ok {
		for key, value := range existingRouting {
			routing[key] = value
		}
	}

	rules := []any{
		map[string]any{
			"inboundTag":  []any{apiInboundTag},
			"outboundTag": apiTag,
		},
	}
	rules = append(rules, arrayFrom(routing["rules"])...)
	routing["rules"] = rules

	return routing
}

func arrayFrom(value any) []any {
	if value == nil {
		return []any{}
	}
	if typed, ok := value.([]any); ok {
		return typed
	}
	return []any{}
}

func pemLines(value string) []any {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	parts := strings.Split(normalized, "\n")
	lines := make([]any, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
