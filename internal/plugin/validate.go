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

	validators := []struct {
		name     string
		validate func(any) error
	}{
		{name: "sharedLists", validate: validateSharedLists},
		{name: "ingressFilter", validate: validateIngressFilterSection},
		{name: "egressFilter", validate: validateEgressFilterSection},
		{name: "connectionDrop", validate: validateConnectionDropSection},
		{name: "torrentBlocker", validate: validateTorrentBlockerSection},
	}
	for _, validator := range validators {
		raw, present := config[validator.name]
		if !present {
			continue
		}
		if err := validator.validate(raw); err != nil {
			return err
		}
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
