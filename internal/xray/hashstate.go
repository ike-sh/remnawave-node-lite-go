package xray

import (
	"log/slog"
	"sort"
)

func (m *Manager) extractUsersFromConfigLocked(hashes ConfigHash, config map[string]any) {
	m.inboundHashes = make(map[string]*HashedSet)
	m.inboundTags = make(map[string]struct{})
	m.emptyConfigHash = hashes.EmptyConfig

	validTags := make(map[string]struct{}, len(hashes.Inbounds))
	for _, inbound := range hashes.Inbounds {
		if inbound.Tag != "" {
			validTags[inbound.Tag] = struct{}{}
		}
	}

	rawInbounds, ok := config["inbounds"].([]any)
	if !ok {
		return
	}

	for _, item := range rawInbounds {
		inbound, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := inbound["tag"].(string)
		if tag == "" {
			continue
		}
		if _, allowed := validTags[tag]; !allowed {
			continue
		}

		ids := extractClientIDs(inbound)
		set := NewHashedSet(ids...)
		m.inboundHashes[tag] = set
		m.inboundTags[tag] = struct{}{}
		slog.Debug("extracted inbound users", "tag", tag, "count", set.Size(), "hash", set.Hash64String())
	}
}

func (m *Manager) isNeedRestartCoreLocked(incoming ConfigHash) bool {
	if m.emptyConfigHash == "" {
		return true
	}
	if incoming.EmptyConfig != m.emptyConfigHash {
		slog.Warn("detected changes in Xray Core base configuration")
		return true
	}
	if len(incoming.Inbounds) != len(m.inboundHashes) {
		slog.Warn("number of Xray Core inbounds has changed")
		return true
	}

	for tag, usersSet := range m.inboundHashes {
		var incomingInbound *InboundHash
		for i := range incoming.Inbounds {
			if incoming.Inbounds[i].Tag == tag {
				incomingInbound = &incoming.Inbounds[i]
				break
			}
		}
		if incomingInbound == nil {
			slog.Warn("inbound no longer exists in Xray Core configuration", "tag", tag)
			return true
		}
		if usersSet.Hash64String() != incomingInbound.Hash {
			slog.Warn(
				"user configuration changed for inbound",
				"tag", tag,
				"current", usersSet.Hash64String(),
				"incoming", incomingInbound.Hash,
			)
			return true
		}
	}

	slog.Info("Xray Core configuration is up-to-date - no restart required")
	return false
}

func (m *Manager) AddUserToInboundHash(inboundTag, userUUID string) {
	if inboundTag == "" || userUUID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	set, ok := m.inboundHashes[inboundTag]
	if !ok {
		if m.inboundHashes == nil {
			m.inboundHashes = make(map[string]*HashedSet)
		}
		if m.inboundTags == nil {
			m.inboundTags = make(map[string]struct{})
		}
		set = NewHashedSet()
		m.inboundHashes[inboundTag] = set
	}
	set.Add(userUUID)
	m.inboundTags[inboundTag] = struct{}{}
}

func (m *Manager) RemoveUserFromInboundHash(inboundTag, userUUID string) {
	if inboundTag == "" || userUUID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	set, ok := m.inboundHashes[inboundTag]
	if !ok {
		return
	}
	set.Delete(userUUID)
	if set.Size() == 0 {
		delete(m.inboundHashes, inboundTag)
		delete(m.inboundTags, inboundTag)
		slog.Warn("inbound has no users, clearing hash map", "tag", inboundTag)
	}
}

func (m *Manager) clearHashStateLocked() {
	m.emptyConfigHash = ""
	m.inboundHashes = nil
}

func extractClientIDs(inbound map[string]any) []string {
	settings, ok := inbound["settings"].(map[string]any)
	if !ok {
		return nil
	}
	clients, ok := settings["clients"].([]any)
	if !ok {
		return nil
	}

	ids := make([]string, 0, len(clients))
	for _, item := range clients {
		client, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := client["id"].(string)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
