package plugin

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type pluginPlan struct {
	snapshot                      *pluginSnapshot
	torrentIncludeRuleTags        []string
	torrentIncludeRuleTagsPresent bool
	diagnostics                   planDiagnostics
}

type planDiagnostics struct {
	firewallUnavailable bool
	asnUnavailable      bool
	missingASNs         map[uint32]struct{}
	missingSharedLists  map[string]struct{}
}

func buildPluginPlan(request *SyncPlugin, resolver ASNResolver, firewallAvailable bool) (*pluginPlan, error) {
	if request == nil {
		return nil, fmt.Errorf("plugin is required")
	}

	var config map[string]any
	if err := json.Unmarshal(request.Config, &config); err != nil || config == nil {
		if err == nil {
			err = fmt.Errorf("config must be an object")
		}
		return nil, fmt.Errorf("decode plugin config: %w", err)
	}
	if err := ValidatePluginConfig(config); err != nil {
		return nil, err
	}

	diagnostics := planDiagnostics{}
	shared := buildSharedIPMapWithDiagnostics(config, resolver, &diagnostics)
	snapshot := &pluginSnapshot{
		configHash:   hashPluginConfig(request.Config),
		pluginUUID:   request.UUID,
		pluginName:   request.Name,
		whitelistIPs: make(map[string]struct{}),
		torrent: torrentSettings{
			ignoredIPs:   make(map[string]struct{}),
			ignoredUsers: make(map[string]struct{}),
		},
	}

	if connectionDrop, ok := config["connectionDrop"].(map[string]any); ok {
		if enabled, _ := connectionDrop["enabled"].(bool); enabled {
			for _, ip := range resolveIPList(toStringSlice(connectionDrop["whitelistIps"]), shared, &diagnostics) {
				snapshot.whitelistIPs[ip] = struct{}{}
			}
		}
	}

	plan := &pluginPlan{snapshot: snapshot, diagnostics: diagnostics}
	if blocker, ok := config["torrentBlocker"].(map[string]any); ok {
		plan.torrentIncludeRuleTags, plan.torrentIncludeRuleTagsPresent = optionalStringSlice(blocker, "includeRuleTags")
		if enabled, _ := blocker["enabled"].(bool); enabled {
			if !firewallAvailable {
				plan.diagnostics.firewallUnavailable = true
			} else {
				snapshot.torrent.enabled = true
				snapshot.torrent.blockDuration, _ = numberValue(blocker["blockDuration"])
				snapshot.torrent.includeRuleTags = append([]string(nil), plan.torrentIncludeRuleTags...)
				if ignore, ok := blocker["ignoreLists"].(map[string]any); ok {
					for _, ip := range resolveIPList(toStringSlice(ignore["ip"]), shared, &plan.diagnostics) {
						snapshot.torrent.ignoredIPs[ip] = struct{}{}
					}
					for _, user := range toNumberStringSlice(ignore["userId"]) {
						snapshot.torrent.ignoredUsers[user] = struct{}{}
					}
				}
			}
		}
	}

	snapshot.firewall = buildFirewallConfig(config, shared, &plan.diagnostics)
	if !firewallAvailable && firewallConfigRequested(config) {
		plan.diagnostics.firewallUnavailable = true
	}
	return plan, nil
}

func buildSharedIPMap(config map[string]any, resolver ASNResolver) map[string][]string {
	return buildSharedIPMapWithDiagnostics(config, resolver, nil)
}

func buildSharedIPMapWithDiagnostics(config map[string]any, resolver ASNResolver, diagnostics *planDiagnostics) map[string][]string {
	shared := make(map[string][]string)
	lists, ok := config["sharedLists"].([]any)
	if !ok {
		return shared
	}
	for _, item := range lists {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		switch entryType, _ := entry["type"].(string); entryType {
		case "ipList":
			shared[name] = toStringSlice(entry["items"])
		case "asList":
			shared[name] = resolveASListWithDiagnostics(entry["items"], resolver, diagnostics)
		}
	}
	return shared
}

func resolveASListWithDiagnostics(rawItems any, resolver ASNResolver, diagnostics *planDiagnostics) []string {
	asns := toASNSlice(rawItems)
	if resolver == nil {
		if diagnostics != nil && len(asns) != 0 {
			diagnostics.asnUnavailable = true
		}
		return nil
	}
	out := make([]string, 0, len(asns))
	for _, asn := range asns {
		v4, v6 := resolver.PrefixesByASN(asn)
		if len(v4) == 0 && len(v6) == 0 {
			if diagnostics != nil {
				diagnostics.addMissingASN(asn)
			}
			continue
		}
		out = append(out, v4...)
		out = append(out, v6...)
	}
	return out
}

func resolveIPList(items []string, shared map[string][]string, diagnostics *planDiagnostics) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item, "ext:") {
			resolved, ok := shared[item]
			if !ok {
				if diagnostics != nil {
					diagnostics.addMissingSharedList(item)
				}
				continue
			}
			out = append(out, resolved...)
			continue
		}
		out = append(out, item)
	}
	return out
}

func buildFirewallConfig(config map[string]any, shared map[string][]string, diagnostics *planDiagnostics) firewallConfig {
	var firewall firewallConfig
	if ingress, ok := config["ingressFilter"].(map[string]any); ok {
		if enabled, _ := ingress["enabled"].(bool); enabled {
			firewall.ingressIPs = resolveIPList(toStringSlice(ingress["blockedIps"]), shared, diagnostics)
		}
	}
	if egress, ok := config["egressFilter"].(map[string]any); ok {
		if enabled, _ := egress["enabled"].(bool); enabled {
			firewall.egressIPs = resolveIPList(toStringSlice(egress["blockedIps"]), shared, diagnostics)
			firewall.egressPorts = toIntSlice(egress["blockedPorts"])
		}
	}
	return firewall
}

func firewallConfigRequested(config map[string]any) bool {
	for _, key := range []string{"ingressFilter", "egressFilter"} {
		section, ok := config[key].(map[string]any)
		if !ok {
			continue
		}
		if enabled, _ := section["enabled"].(bool); enabled {
			return true
		}
	}
	return false
}

func optionalStringSlice(object map[string]any, key string) ([]string, bool) {
	raw, present := object[key]
	if !present {
		return nil, false
	}
	return toStringSlice(raw), true
}

func toStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if str, ok := item.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func toNumberStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if number, ok := numberValue(item); ok {
			out = append(out, strconv.FormatFloat(number, 'f', -1, 64))
		}
	}
	return out
}

func toIntSlice(value any) []int {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		if number, ok := numberValue(item); ok {
			out = append(out, int(number))
		}
	}
	return out
}

func (d *planDiagnostics) addMissingASN(asn uint32) {
	if d.missingASNs == nil {
		d.missingASNs = make(map[uint32]struct{})
	}
	d.missingASNs[asn] = struct{}{}
}

func (d *planDiagnostics) addMissingSharedList(name string) {
	if d.missingSharedLists == nil {
		d.missingSharedLists = make(map[string]struct{})
	}
	d.missingSharedLists[name] = struct{}{}
}

func (d planDiagnostics) missingASNValues() []uint32 {
	values := make([]uint32, 0, len(d.missingASNs))
	for value := range d.missingASNs {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values
}

func (d planDiagnostics) missingSharedListValues() []string {
	values := make([]string, 0, len(d.missingSharedLists))
	for value := range d.missingSharedLists {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
