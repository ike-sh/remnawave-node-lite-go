package plugin

import (
	"encoding/json"
	"fmt"
)

// ValidatePluginConfig performs structural validation aligned with @remnawave/node-plugins@0.4.5.
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

func pluginConfigHash(config json.RawMessage) string {
	return hashPluginConfig(config)
}

func isUnchangedPluginConfig(plugin *SyncPlugin, state *State) bool {
	if plugin == nil || state == nil {
		return false
	}
	hash := pluginConfigHash(plugin.Config)
	return hash != "" && hash == state.ConfigHash() && state.HasActivePlugin()
}

func extractPluginConfig(plugin *SyncPlugin) map[string]any {
	if plugin == nil || len(plugin.Config) == 0 {
		return nil
	}
	var config map[string]any
	if err := json.Unmarshal(plugin.Config, &config); err != nil {
		return nil
	}
	return config
}
