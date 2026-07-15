package xray

import (
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

func TestHashedSetMatchesReference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		items []string
		want  string
	}{
		{"empty", nil, "0000000000000000"},
		{"single uuid", []string{"66ad4540-b58c-4ad2-9926-ea63445a9b57"}, "75ccc662-47b26544"},
		{
			"two uuids",
			[]string{
				"66ad4540-b58c-4ad2-9926-ea63445a9b57",
				"a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			},
			"-4f6b8c1-b058f61",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set := NewHashedSet(tc.items...)
			if got := set.Hash64String(); got != tc.want {
				t.Fatalf("Hash64String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatJavaScriptHashWordMatchesSignedPadStart(t *testing.T) {
	t.Parallel()

	cases := map[uint32]string{
		0:          "00000000",
		0x75ccc662: "75ccc662",
		0xb84d9abc: "-47b26544",
		0x80000000: "-80000000",
		0xffffffff: "000000-1",
	}
	for input, want := range cases {
		if got := formatJavaScriptHashWord(input); got != want {
			t.Errorf("formatJavaScriptHashWord(%08x) = %q, want %q", input, got, want)
		}
	}
}

func TestHashedSetAddDelete(t *testing.T) {
	t.Parallel()

	set := NewHashedSet("66ad4540-b58c-4ad2-9926-ea63445a9b57", "a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	initial := set.Hash64String()

	set.Add("66ad4540-b58c-4ad2-9926-ea63445a9b57")
	if got := set.Hash64String(); got != initial {
		t.Fatalf("duplicate add changed hash: %q -> %q", initial, got)
	}

	set.Delete("66ad4540-b58c-4ad2-9926-ea63445a9b57")
	if got := set.Hash64String(); got == initial {
		t.Fatalf("delete should change hash")
	}
	if set.Size() != 1 {
		t.Fatalf("expected size 1, got %d", set.Size())
	}
}

func TestIsNeedRestartCore(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	manager.extractUsersFromConfigLocked(ConfigHash{
		EmptyConfig: "base-hash",
		Inbounds: []InboundHash{
			{Tag: "in-1", Hash: hash64FromItems([]string{"uuid-1"})},
		},
	}, map[string]any{
		"inbounds": []any{
			map[string]any{
				"tag": "in-1",
				"settings": map[string]any{
					"clients": []any{
						map[string]any{"id": "uuid-1"},
					},
				},
			},
		},
	})

	if manager.isNeedRestartCoreLocked(ConfigHash{
		EmptyConfig: "base-hash",
		Inbounds: []InboundHash{
			{Tag: "in-1", Hash: hash64FromItems([]string{"uuid-1"})},
		},
	}) {
		t.Fatal("expected no restart for unchanged hash")
	}

	if !manager.isNeedRestartCoreLocked(ConfigHash{
		EmptyConfig: "other-base",
		Inbounds: []InboundHash{
			{Tag: "in-1", Hash: hash64FromItems([]string{"uuid-1"})},
		},
	}) {
		t.Fatal("expected restart when emptyConfig changes")
	}

	if !manager.isNeedRestartCoreLocked(ConfigHash{
		EmptyConfig: "base-hash",
		Inbounds: []InboundHash{
			{Tag: "in-1", Hash: hash64FromItems([]string{"uuid-2"})},
		},
	}) {
		t.Fatal("expected restart when inbound hash changes")
	}
}

func TestAddRemoveUserFromInboundHash(t *testing.T) {
	t.Parallel()

	manager := &Manager{state: lifecycleRunning, generation: 7}
	result := xtls.HandlerResult{OK: true, Generation: 7}
	if !manager.CommitUserAdded(result, "in-1", "uuid-1") ||
		!manager.CommitUserAdded(result, "in-1", "uuid-2") {
		t.Fatal("expected current generation hash commits to succeed")
	}
	if len(manager.InboundTags()) != 1 {
		t.Fatalf("expected one inbound tag, got %v", manager.InboundTags())
	}

	if !manager.CommitUserRemoved(result, "in-1", "uuid-1") ||
		!manager.CommitUserRemoved(result, "in-1", "uuid-2") {
		t.Fatal("expected current generation hash commits to succeed")
	}
	if len(manager.InboundTags()) != 0 {
		t.Fatalf("expected inbound cleared, got %v", manager.InboundTags())
	}
}

func TestHashCommitRejectsStaleGeneration(t *testing.T) {
	t.Parallel()

	manager := &Manager{state: lifecycleRunning, generation: 8}
	if manager.CommitUserAdded(xtls.HandlerResult{OK: true, Generation: 7}, "in-1", "uuid-1") {
		t.Fatal("stale generation must not update hash state")
	}
	if len(manager.InboundTags()) != 0 {
		t.Fatalf("stale commit published inbound tags: %v", manager.InboundTags())
	}
}
