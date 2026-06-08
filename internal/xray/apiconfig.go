package xray

import (
	"strings"

	"remnawave-node-lite-go/internal/netadmin"
)

const (
	apiTag                       = "REMNAWAVE_API"
	apiInboundTag                = "REMNAWAVE_API_INBOUND"
	torrentBlockerOutboundTag    = "RW_TB_OUTBOUND_BLOCK"
)

type TorrentBlockerOptions struct {
	Enabled         bool
	IncludeRuleTags []string
	SocketPath      string
	RESTToken       string
}

func generateAPIConfig(input map[string]any, xtlsAPIPort int, certs internalCerts, torrent TorrentBlockerOptions) map[string]any {
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
	result["policy"] = policyFrom(result["policy"], netadmin.HasCapNetAdmin())
	result["routing"] = routingFrom(result["routing"])

	if torrent.Enabled {
		webhookURL := buildWebhookURL(torrent.SocketPath, torrent.RESTToken)
		outbounds := arrayFrom(result["outbounds"])
		outbounds = append(outbounds, map[string]any{
			"tag":      torrentBlockerOutboundTag,
			"protocol": "blackhole",
		})
		result["outbounds"] = outbounds

		routing, _ := result["routing"].(map[string]any)
		rules := arrayFrom(routing["rules"])
		torrentRule := map[string]any{
			"protocol":    []any{"bittorrent"},
			"outboundTag": torrentBlockerOutboundTag,
			"webhook": map[string]any{
				"url":             webhookURL,
				"deduplication": 5,
			},
		}
		if len(rules) == 0 {
			rules = []any{torrentRule}
		} else {
			inserted := make([]any, 0, len(rules)+1)
			inserted = append(inserted, rules[0], torrentRule)
			inserted = append(inserted, rules[1:]...)
			rules = inserted
		}

		if len(torrent.IncludeRuleTags) > 0 {
			tagSet := make(map[string]struct{}, len(torrent.IncludeRuleTags))
			for _, tag := range torrent.IncludeRuleTags {
				tagSet[tag] = struct{}{}
			}
			for i, item := range rules {
				rule, ok := item.(map[string]any)
				if !ok {
					continue
				}
				ruleTag, _ := rule["ruleTag"].(string)
				if _, ok := tagSet[ruleTag]; ok {
					rule["webhook"] = map[string]any{
						"url":             webhookURL,
						"deduplication": 5,
					}
					rules[i] = rule
				}
			}
		}
		routing["rules"] = rules
		result["routing"] = routing
	}

	return result
}

func buildWebhookURL(socketPath, token string) string {
	return "/" + socketPath + ":/internal/webhook?token=" + token
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

func policyFrom(existing any, statsUserOnline bool) map[string]any {
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
	levelZero["statsUserOnline"] = statsUserOnline

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
