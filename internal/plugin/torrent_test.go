package plugin

import (
	"sync"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xraywebhook"
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
	service.HandleXrayWebhook(xraywebhook.Payload{
		Email:       xraywebhook.String("user-1"),
		Source:      xraywebhook.String("tcp:203.0.113.10:443"),
		Network:     xraywebhook.String("tcp"),
		Destination: xraywebhook.String("198.51.100.1:443"),
		Timestamp:   xraywebhook.Number(123),
	})

	deadline := time.Now().Add(time.Second)
	for state.ReportsCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("reports count = %d, want 1", state.ReportsCount())
		}
		time.Sleep(time.Millisecond)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.blockCalls) != 1 || len(backend.blockCalls[0]) != 1 {
		t.Fatalf("block calls = %#v", backend.blockCalls)
	}
}

func TestCloseDropsQueuedWebhookWithoutProcessingIt(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if response := service.Sync(torrentPlugin(t, true, nil)); !response.Accepted {
		t.Fatal("torrent sync failed")
	}
	service.operationGate <- struct{}{}
	for range 3 {
		service.HandleXrayWebhook(xraywebhook.Payload{
			Email:  xraywebhook.String("user-1"),
			Source: xraywebhook.String("tcp:203.0.113.10:443"),
		})
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- service.Close() }()
	deadline := time.Now().Add(time.Second)
	for !service.webhookStopped.Load() {
		if time.Now().After(deadline) {
			t.Fatal("Close did not stop webhook intake")
		}
		time.Sleep(time.Millisecond)
	}
	<-service.operationGate
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	blockCalls := len(backend.blockCalls)
	backend.mu.Unlock()
	if blockCalls != 0 || state.ReportsCount() != 0 {
		t.Fatalf("queued webhook ran during Close: blocks=%d reports=%d", blockCalls, state.ReportsCount())
	}
	if len(service.webhookQueue) != 0 {
		t.Fatalf("Close retained %d queued webhooks", len(service.webhookQueue))
	}
	service.HandleXrayWebhook(xraywebhook.Payload{})
	if len(service.webhookQueue) != 0 {
		t.Fatal("webhook was queued after Close")
	}
}

func TestWebhookStopWaitsForInFlightAdmission(t *testing.T) {
	t.Parallel()

	service := newServiceWithBackend(NewState(), nil, nil, &fakeFirewall{})
	t.Cleanup(func() { _ = service.Close() })
	service.webhookAdmissionMu.RLock()
	stopDone := make(chan struct{})
	go func() {
		service.signalWebhookStop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		service.webhookAdmissionMu.RUnlock()
		t.Fatal("webhook stop crossed an in-flight admission")
	case <-time.After(20 * time.Millisecond):
	}
	service.webhookAdmissionMu.RUnlock()
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("webhook stop did not finish after admission released")
	}
	service.HandleXrayWebhook(xraywebhook.Payload{})
	if len(service.webhookQueue) != 0 {
		t.Fatal("webhook was queued after the admission fence closed")
	}
}

func TestHandleXrayWebhookQueuesWithoutWaitingForPluginMutation(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if response := service.Sync(torrentPlugin(t, true, nil)); !response.Accepted {
		t.Fatal("torrent sync failed")
	}
	applyStarted := make(chan struct{})
	releaseApply := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseApply) }) })
	backend.setApplyHook(func(call int) {
		if call == 2 {
			close(applyStarted)
			<-releaseApply
		}
	})
	syncDone := make(chan AcceptedResponse, 1)
	go func() { syncDone <- service.Sync(torrentAndFilterPlugin(t, "192.0.2.0/24")) }()
	select {
	case <-applyStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plugin mutation")
	}

	started := time.Now()
	service.HandleXrayWebhook(xraywebhook.Payload{
		Email:  xraywebhook.String("user-1"),
		Source: xraywebhook.String("tcp:203.0.113.10:443"),
	})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("webhook waited behind plugin mutation for %s", elapsed)
	}
	releaseOnce.Do(func() { close(releaseApply) })
	if response := <-syncDone; !response.Accepted {
		t.Fatal("sync was not accepted")
	}
	deadline := time.Now().Add(time.Second)
	for state.ReportsCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("queued webhook was not processed")
		}
		time.Sleep(time.Millisecond)
	}
}
