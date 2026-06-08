package xray

func extractInboundTags(config map[string]any) []string {
	raw, ok := config["inbounds"]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}

	tags := make([]string, 0, len(items))
	for _, item := range items {
		inbound, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tag, ok := inbound["tag"].(string)
		if !ok || tag == "" {
			continue
		}
		tags = append(tags, tag)
	}
	return tags
}

func (m *Manager) resetInboundTags(tags []string) {
	m.inboundTags = make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		m.inboundTags[tag] = struct{}{}
	}
}

func (m *Manager) AddInboundTag(tag string) {
	if tag == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inboundTags == nil {
		m.inboundTags = make(map[string]struct{})
	}
	m.inboundTags[tag] = struct{}{}
}

func (m *Manager) InboundTags() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.inboundTags) == 0 {
		return nil
	}
	tags := make([]string, 0, len(m.inboundTags))
	for tag := range m.inboundTags {
		tags = append(tags, tag)
	}
	return tags
}

func (m *Manager) clearInboundTagsLocked() {
	m.inboundTags = nil
}
