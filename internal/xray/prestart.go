package xray

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

const maxPreStartMatches = 256

func (m *Manager) runPreStart() {
	m.mu.RLock()
	provider := m.torrentBlocker
	m.mu.RUnlock()
	if provider == nil {
		return
	}
	enabled, patterns := provider.PreStartCleanupSockets()
	if !enabled || len(patterns) == 0 {
		return
	}

	removed := 0
	for _, pattern := range patterns {
		for _, path := range resolveSocketPattern(pattern) {
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				log.Printf("warning: pre-start inspect socket %s: %v", path, err)
				continue
			}
			if info.Mode()&os.ModeSocket == 0 {
				log.Printf("pre-start socket cleanup skipped non-socket %s", path)
				continue
			}
			if err := os.Remove(path); err != nil {
				log.Printf("warning: pre-start remove socket %s: %v", path, err)
				continue
			}
			removed++
			log.Printf("pre-start removed stale socket %s", path)
		}
	}
	log.Printf("pre-start socket cleanup completed: %d removed", removed)
}

func resolveSocketPattern(pattern string) []string {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		log.Printf("warning: pre-start invalid socket glob %q: %v", pattern, err)
		return nil
	}
	if len(matches) > maxPreStartMatches {
		log.Printf("warning: pre-start socket glob %q exceeded %d matches; remaining entries skipped", pattern, maxPreStartMatches)
		matches = matches[:maxPreStartMatches]
	}
	return matches
}
