package plugin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestHandleXrayWebhookPostsAdditionalWebhook(t *testing.T) {
	received := make(chan plugin.TorrentReport, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: method=%s content-type=%s", r.Method, r.Header.Get("Content-Type"))
		}
		var report plugin.TorrentReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Errorf("decode report: %v", err)
		}
		received <- report
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	state := plugin.NewState()
	payload, err := plugin.NewSyncPluginFromEnvelope(map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": float64(60),
				"webhookUrl":    server.URL,
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

	select {
	case report := <-received:
		if report.ActionReport.IP != "203.0.113.10" || report.ActionReport.UserID != "user-1" {
			t.Fatalf("unexpected webhook report: %#v", report)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("additional torrent webhook was not delivered")
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

func TestHandleXrayWebhookPreservesPermanentBlockDuration(t *testing.T) {
	state := plugin.NewState()
	payload, err := plugin.NewSyncPluginFromEnvelope(map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"torrentBlocker": map[string]any{
				"enabled":       true,
				"blockDuration": float64(0),
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
		"source": "203.0.113.10",
	})
	reports := state.FlushReports()
	if len(reports) != 1 || reports[0].ActionReport.BlockDuration != 0 {
		t.Fatalf("permanent block duration changed: %#v", reports)
	}
}
