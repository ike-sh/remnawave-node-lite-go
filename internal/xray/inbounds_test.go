package xray

import "testing"

func TestExtractInboundTags(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{
		"inbounds": []any{
			map[string]any{"tag": "in-1", "protocol": "vless"},
			map[string]any{"tag": "", "protocol": "trojan"},
			map[string]any{"protocol": "vmess"},
			map[string]any{"tag": "in-2"},
		},
	}

	got := extractInboundTags(cfg)
	if len(got) != 2 {
		t.Fatalf("expected 2 tags, got %d: %v", len(got), got)
	}
	if got[0] != "in-1" && got[1] != "in-1" {
		t.Fatalf("missing in-1: %v", got)
	}
	if got[0] != "in-2" && got[1] != "in-2" {
		t.Fatalf("missing in-2: %v", got)
	}
}

func TestManagerInboundTags(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	manager.resetInboundTags([]string{"a", "b"})
	manager.AddInboundTag("c")

	tags := manager.InboundTags()
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags, got %d: %v", len(tags), tags)
	}
}
