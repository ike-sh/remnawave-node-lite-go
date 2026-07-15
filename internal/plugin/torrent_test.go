package plugin

import "testing"

func TestExtractWebhookIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source string
		want   string
	}{
		{"tcp:203.0.113.10:443", "203.0.113.10"},
		{"udp:[2001:db8::1]:53", "2001:db8::1"},
		{"203.0.113.20", "203.0.113.20"},
		{"invalid", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.source, func(t *testing.T) {
			t.Parallel()
			if got := extractWebhookIP(tc.source); got != tc.want {
				t.Fatalf("extractWebhookIP(%q) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}

func TestHandleXrayWebhookBlocksAndAddsReport(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if response := service.Sync(torrentPlugin(t, true, nil)); !response.Accepted {
		t.Fatal("torrent sync failed")
	}
	service.HandleXrayWebhook(map[string]any{
		"email":  "user-1",
		"source": "tcp:203.0.113.10:443",
	})

	if state.ReportsCount() != 1 {
		t.Fatalf("reports count = %d, want 1", state.ReportsCount())
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.blockCalls) != 1 || len(backend.blockCalls[0]) != 1 {
		t.Fatalf("block calls = %#v", backend.blockCalls)
	}
}
