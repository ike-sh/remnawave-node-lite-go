package xray

import (
	"remnawave-node-lite-go/internal/netadmin"
)

const (
	apiTag                    = "REMNAWAVE_API"
	apiInboundTag             = "REMNAWAVE_API_INBOUND"
	torrentBlockerOutboundTag = "RW_TB_OUTBOUND_BLOCK"
)

type TorrentBlockerOptions struct {
	Enabled         bool
	IncludeRuleTags []string
	SocketPath      string
	RESTToken       string
}

func generateAPIConfig(input map[string]any, xtlsSocket string, torrent TorrentBlockerOptions) map[string]any {
	result := cloneMap(input)

	result["stats"] = map[string]any{}
	result["api"] = map[string]any{
		"services": []any{"HandlerService", "StatsService", "RoutingService"},
		"tag":      apiTag,
	}
	result["inbounds"] = append(
		[]any{apiInbound(xtlsSocket)},
		arrayFrom(result["inbounds"])...,
	)
	result["outbounds"] = arrayFrom(result["outbounds"])
	result["policy"] = policyFrom(result["policy"], netadmin.HasCapNetAdmin())
	result["routing"] = routingFrom(result["routing"])

	if torrent.Enabled {
		webhookURL := buildWebhookURL(torrent.SocketPath)
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
				"url":           webhookURL,
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
						"url":           webhookURL,
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

func buildWebhookURL(socketPath string) string {
	return "/" + socketPath + ":/internal/webhook"
}

// apiInbound exposes the Xray gRPC API on a Linux abstract unix socket via the
// tunnel inbound, matching remnawave/node 2.8.0 (XRAY_API_INBOUND_MODEL). The
// leading "@" selects the abstract namespace; a local socket needs no TLS.
func apiInbound(socketName string) map[string]any {
	return map[string]any{
		"tag":      apiInboundTag,
		"listen":   "@" + socketName,
		"protocol": "tunnel",
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
	// Drop any pre-existing REMNAWAVE_API rule before re-injecting ours, matching
	// upstream generate-api-config.ts (prevents duplicate API routing rules).
	for _, item := range arrayFrom(routing["rules"]) {
		if rule, ok := item.(map[string]any); ok {
			if tag, _ := rule["outboundTag"].(string); tag == apiTag {
				continue
			}
		}
		rules = append(rules, item)
	}
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
