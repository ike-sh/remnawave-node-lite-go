package plugin

import "strings"

type preStartSettings struct {
	enabled        bool
	cleanupEnabled bool
	files          []string
}

func (s *State) configurePreStart(rawConfig map[string]any) {
	s.preStart = preStartSettings{}
	section, ok := rawConfig["preStart"].(map[string]any)
	if !ok {
		return
	}
	s.preStart.enabled, _ = section["enabled"].(bool)
	cleanup, ok := section["cleanupSockets"].(map[string]any)
	if !ok {
		return
	}
	s.preStart.cleanupEnabled, _ = cleanup["enabled"].(bool)
	for _, file := range toStringSlice(cleanup["files"]) {
		if trimmed := strings.TrimSpace(file); trimmed != "" {
			s.preStart.files = append(s.preStart.files, trimmed)
		}
	}
}

// PreStartCleanupSockets returns a copy of the active cleanup configuration.
// It intentionally uses primitive return values so xray can consume it without
// introducing a package dependency cycle.
func (s *State) PreStartCleanupSockets() (bool, []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.preStart.enabled || !s.preStart.cleanupEnabled {
		return false, nil
	}
	return true, append([]string(nil), s.preStart.files...)
}
