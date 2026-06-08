package plugin_test

import (
	"testing"

	"remnawave-node-lite-go/internal/plugin"
)

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
			if got := plugin.ExtractWebhookIPForTest(tc.source); got != tc.want {
				t.Fatalf("extractWebhookIP(%q) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}

func TestHandleXrayWebhookAddsReport(t *testing.T) {
	t.Parallel()

	state := plugin.NewState()
	payload, err := plugin.NewSyncPluginFromEnvelope(map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": float64(60),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = state.UpdateFromSync(payload)
	service := plugin.NewService(state, nil, nil)
	service.HandleXrayWebhook(map[string]any{
		"email":  "user-1",
		"source": "tcp:203.0.113.10:443",
	})

	if state.ReportsCount() != 1 {
		t.Fatalf("expected one report, got %d", state.ReportsCount())
	}
}
