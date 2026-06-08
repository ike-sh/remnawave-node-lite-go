package plugin

import (
	"fmt"
	"strings"
)

// ValidatePluginConfig performs structural validation aligned with @remnawave/node-plugins@0.4.4.
func ValidatePluginConfig(config map[string]any) error {
	if config == nil {
		return fmt.Errorf("plugin config is required")
	}

	if err := validateSharedLists(config["sharedLists"]); err != nil {
		return err
	}
	if err := validateIngressFilterSection(config["ingressFilter"]); err != nil {
		return err
	}
	if err := validateEgressFilterSection(config["egressFilter"]); err != nil {
		return err
	}
	if err := validateConnectionDropSection(config["connectionDrop"]); err != nil {
		return err
	}
	if err := validateTorrentBlockerSection(config["torrentBlocker"]); err != nil {
		return err
	}

	return nil
}

func pluginConfigHash(plugin map[string]any) string {
	if plugin == nil {
		return ""
	}
	rawConfig, _ := plugin["config"].(map[string]any)
	return hashConfig(rawConfig)
}

func isUnchangedPluginConfig(plugin map[string]any, state *State) bool {
	if plugin == nil || state == nil {
		return false
	}
	hash := pluginConfigHash(plugin)
	return hash != "" && hash == state.ConfigHash() && state.HasActivePlugin()
}

func extractPluginConfig(plugin map[string]any) map[string]any {
	if plugin == nil {
		return nil
	}
	raw, _ := plugin["config"].(map[string]any)
	return raw
}

func pluginUUID(plugin map[string]any) string {
	if plugin == nil {
		return ""
	}
	uuid, _ := plugin["uuid"].(string)
	return strings.TrimSpace(uuid)
}
