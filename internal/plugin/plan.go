package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type pluginPlan struct {
	snapshot    *pluginSnapshot
	diagnostics planDiagnostics
}

type planDiagnostics struct {
	firewallUnavailable bool
	asnUnavailable      bool
	missingASNs         map[uint32]struct{}
	missingSharedLists  map[string]struct{}
}

func buildPluginPlan(request *SyncPlugin, resolver ASNResolver, firewallAvailable bool) (*pluginPlan, error) {
	return buildPluginPlanContext(context.Background(), request, resolver, firewallAvailable)
}

func buildPluginPlanContext(ctx context.Context, request *SyncPlugin, resolver ASNResolver, firewallAvailable bool) (*pluginPlan, error) {
	config, configHash, err := decodePluginConfigContext(ctx, request)
	if err != nil {
		return nil, err
	}
	return buildPluginPlanFromConfigContext(ctx, request, config, configHash, resolver, firewallAvailable)
}

func decodePluginConfigContext(ctx context.Context, request *SyncPlugin) (map[string]any, string, error) {
	if request == nil {
		return nil, "", fmt.Errorf("plugin is required")
	}
	if len(request.Config) > maxPluginConfigBytes {
		return nil, "", fmt.Errorf("plugin config is %d bytes; maximum is %d", len(request.Config), maxPluginConfigBytes)
	}
	configHash, err := hashPluginConfigContext(ctx, request.Config)
	if err != nil {
		return nil, "", err
	}

	var config map[string]any
	if err = json.Unmarshal(request.Config, &config); err != nil || config == nil {
		if err == nil {
			err = fmt.Errorf("config must be an object")
		}
		return nil, "", fmt.Errorf("decode plugin config: %w", err)
	}
	if err := ValidatePluginConfig(config); err != nil {
		return nil, "", err
	}
	return config, configHash, nil
}

func buildPluginPlanFromConfigContext(
	ctx context.Context,
	request *SyncPlugin,
	config map[string]any,
	configHash string,
	resolver ASNResolver,
	firewallAvailable bool,
) (*pluginPlan, error) {
	diagnostics := planDiagnostics{}
	shared, err := buildSharedIPMapWithDiagnosticsContext(ctx, config, resolver, &diagnostics)
	if err != nil {
		return nil, err
	}
	snapshot := &pluginSnapshot{
		configHash:    configHash,
		sourceHash:    sha256.Sum256(request.Config),
		pluginUUID:    request.UUID,
		pluginName:    request.Name,
		firewallReady: firewallAvailable,
		torrent: torrentSettings{
			ignoredUsers: make(map[string]struct{}),
		},
	}
	resolvedBudget := expansionBudget{remaining: maxResolvedIPItems}

	if connectionDrop, ok := config["connectionDrop"].(map[string]any); ok {
		if enabled, _ := connectionDrop["enabled"].(bool); enabled {
			resolved, resolveErr := resolveIPListContext(ctx, toStringSlice(connectionDrop["whitelistIps"]), shared, &diagnostics, &resolvedBudget)
			if resolveErr != nil {
				return nil, resolveErr
			}
			snapshot.whitelistIPs = newIPMatcher(resolved)
		}
	}

	plan := &pluginPlan{snapshot: snapshot, diagnostics: diagnostics}
	if blocker, ok := config["torrentBlocker"].(map[string]any); ok {
		includeRuleTags := toStringSlice(blocker["includeRuleTags"])
		snapshot.torrent.includeRuleTags = append([]string(nil), includeRuleTags...)
		if enabled, _ := blocker["enabled"].(bool); enabled {
			snapshot.torrent.enabled = true
			snapshot.torrent.blockDuration, _ = numberValue(blocker["blockDuration"])
			if ignore, ok := blocker["ignoreLists"].(map[string]any); ok {
				resolved, resolveErr := resolveIPListContext(ctx, toStringSlice(ignore["ip"]), shared, &plan.diagnostics, &resolvedBudget)
				if resolveErr != nil {
					return nil, resolveErr
				}
				snapshot.torrent.ignoredIPs = newIPMatcher(resolved)
				for _, user := range toNumberStringSlice(ignore["userId"]) {
					snapshot.torrent.ignoredUsers[user] = struct{}{}
				}
			}
			if !firewallAvailable {
				plan.diagnostics.firewallUnavailable = true
			}
		}
	}

	snapshot.firewall, err = buildFirewallConfigContext(ctx, config, shared, &plan.diagnostics, &resolvedBudget)
	if err != nil {
		return nil, err
	}
	if !firewallAvailable && firewallConfigRequested(config) {
		plan.diagnostics.firewallUnavailable = true
	}
	return plan, nil
}

func buildSharedIPMapWithDiagnosticsContext(ctx context.Context, config map[string]any, resolver ASNResolver, diagnostics *planDiagnostics) (map[string][]string, error) {
	shared := make(map[string][]string)
	lists, ok := config["sharedLists"].([]any)
	if !ok {
		return shared, nil
	}
	budget := expansionBudget{remaining: maxResolvedIPItems}
	for _, item := range lists {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		switch entryType, _ := entry["type"].(string); entryType {
		case "ipList":
			values := toStringSlice(entry["items"])
			if err := budget.consume(len(values)); err != nil {
				return nil, fmt.Errorf("expand shared list %s: %w", quotedForError(name), err)
			}
			shared[name] = values
		case "asList":
			resolved, err := resolveASListWithDiagnosticsContext(ctx, entry["items"], resolver, diagnostics, &budget)
			if err != nil {
				return nil, fmt.Errorf("expand shared list %s: %w", quotedForError(name), err)
			}
			shared[name] = resolved
		}
	}
	return shared, nil
}

func resolveASListWithDiagnosticsContext(
	ctx context.Context,
	rawItems any,
	resolver ASNResolver,
	diagnostics *planDiagnostics,
	budget *expansionBudget,
) ([]string, error) {
	asns := toASNSlice(rawItems)
	if resolver == nil {
		if diagnostics != nil && len(asns) != 0 {
			diagnostics.asnUnavailable = true
		}
		return nil, nil
	}
	out := make([]string, 0, len(asns))
	for _, asn := range asns {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		v4, v6 := resolver.PrefixesByASN(asn)
		if len(v4) == 0 && len(v6) == 0 {
			if diagnostics != nil {
				diagnostics.addMissingASN(asn)
			}
			continue
		}
		if err := budget.consume(len(v4) + len(v6)); err != nil {
			return nil, err
		}
		if err := validateASNPrefixes(v4); err != nil {
			return nil, err
		}
		if err := validateASNPrefixes(v6); err != nil {
			return nil, err
		}
		out = append(out, v4...)
		out = append(out, v6...)
	}
	return out, nil
}

func validateASNPrefixes(prefixes []string) error {
	for _, prefix := range prefixes {
		if err := validateStringLength("resolved ASN prefix", prefix); err != nil {
			return err
		}
		if !isSharedListItem(prefix) {
			return fmt.Errorf("ASN resolver returned invalid prefix %s", quotedForError(prefix))
		}
	}
	return nil
}

func resolveIPListContext(
	ctx context.Context,
	items []string,
	shared map[string][]string,
	diagnostics *planDiagnostics,
	budget *expansionBudget,
) ([]string, error) {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		if strings.HasPrefix(item, "ext:") {
			resolved, ok := shared[item]
			if !ok {
				if diagnostics != nil {
					diagnostics.addMissingSharedList(item)
				}
				continue
			}
			if err := budget.consume(len(resolved)); err != nil {
				return nil, fmt.Errorf("resolve %s: %w", quotedForError(item), err)
			}
			out = append(out, resolved...)
			continue
		}
		if err := budget.consume(1); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func buildFirewallConfigContext(
	ctx context.Context,
	config map[string]any,
	shared map[string][]string,
	diagnostics *planDiagnostics,
	budget *expansionBudget,
) (firewallConfig, error) {
	var firewall firewallConfig
	if ingress, ok := config["ingressFilter"].(map[string]any); ok {
		if enabled, _ := ingress["enabled"].(bool); enabled {
			resolved, err := resolveIPListContext(ctx, toStringSlice(ingress["blockedIps"]), shared, diagnostics, budget)
			if err != nil {
				return firewallConfig{}, err
			}
			firewall.ingressIPs = resolved
		}
	}
	if egress, ok := config["egressFilter"].(map[string]any); ok {
		if enabled, _ := egress["enabled"].(bool); enabled {
			resolved, err := resolveIPListContext(ctx, toStringSlice(egress["blockedIps"]), shared, diagnostics, budget)
			if err != nil {
				return firewallConfig{}, err
			}
			firewall.egressIPs = resolved
			firewall.egressPorts = toIntSlice(egress["blockedPorts"])
		}
	}
	return firewall, nil
}

type expansionBudget struct {
	remaining int
}

func (b *expansionBudget) consume(count int) error {
	if count < 0 || count > b.remaining {
		return fmt.Errorf("resolved IP budget exceeded (%d)", maxResolvedIPItems)
	}
	b.remaining -= count
	return nil
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
