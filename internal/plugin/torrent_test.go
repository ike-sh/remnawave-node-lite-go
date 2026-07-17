package plugin

import (
	"context"
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
		{"tcp:[2001:0db8:0000:0000:0000:0000:0000:0001]:443", "2001:db8::1"},
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

func TestTorrentIgnoreMatcherCanonicalizesAddressesAndCIDRs(t *testing.T) {
	t.Parallel()

	settings := torrentSettings{ignoredIPs: newIPMatcher([]string{
		"2001:0db8:0000:0000:0000:0000:0000:0001",
		"192.0.2.0/24",
	})}
	if !torrentIPIgnored(settings, "2001:db8::1") || !torrentIPIgnored(settings, "192.0.2.42") {
		t.Fatal("canonical IPv6 or CIDR ignore did not match")
	}
	if torrentIPIgnored(settings, "2001:db8::2") || torrentIPIgnored(settings, "198.51.100.1") {
		t.Fatal("ignore matcher matched an address outside its prefixes")
	}
	if !torrentIPIgnored(settings, "127.0.0.2") || !torrentIPIgnored(settings, "::1") {
		t.Fatal("protected loopback address was not ignored")
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

func TestHandleXrayWebhookPreservesPermanentBlockSemantics(t *testing.T) {
	t.Parallel()

	state := NewState()
	service, backend := newReadyService(t, state, nil)
	if response := service.Sync(torrentPluginWithDuration(t, true, 0, nil)); !response.Accepted {
		t.Fatal("permanent torrent config was rejected")
	}
	service.HandleXrayWebhook(xraywebhook.Payload{
		Email:  xraywebhook.String("user-1"),
		Source: xraywebhook.String("tcp:203.0.113.10:443"),
	})

	deadline := time.Now().Add(time.Second)
	for state.ReportsCount() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("permanent torrent webhook was not processed")
		}
		time.Sleep(time.Millisecond)
	}
	backend.mu.Lock()
	if len(backend.blockCalls) != 1 || len(backend.blockCalls[0]) != 1 || backend.blockCalls[0][0].Timeout != 0 {
		calls := backend.blockCalls
		backend.mu.Unlock()
		t.Fatalf("permanent block calls = %#v", calls)
	}
	backend.mu.Unlock()

	reports := service.CollectReports().Reports
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	action := reports[0].ActionReport
	if action.BlockDuration != 0 || !action.WillUnblockAt.Equal(action.ProcessedAt) {
		t.Fatalf("permanent report = %#v", action)
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

func TestHandleXrayWebhookWaitsForBoundedQueueCapacity(t *testing.T) {
	t.Parallel()

	service := newServiceWithBackend(NewState(), nil, nil, &fakeFirewall{})
	t.Cleanup(func() { _ = service.Close() })
	service.operationGate <- struct{}{}
	gateReleased := false
	t.Cleanup(func() {
		if !gateReleased {
			<-service.operationGate
		}
	})

	if !service.HandleXrayWebhook(xraywebhook.Payload{}) {
		t.Fatal("first webhook was not admitted")
	}
	deadline := time.Now().Add(time.Second)
	for len(service.webhookQueue) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker did not take first webhook")
		}
		time.Sleep(time.Millisecond)
	}
	for range cap(service.webhookQueue) {
		service.webhookQueue <- queuedWebhook{}
	}
	result := make(chan bool, 1)
	go func() {
		result <- service.HandleXrayWebhookContext(context.Background(), xraywebhook.Payload{})
	}()
	select {
	case <-result:
		t.Fatal("webhook admission returned while the bounded queue was full")
	case <-time.After(20 * time.Millisecond):
	}
	<-service.webhookQueue
	select {
	case accepted := <-result:
		if !accepted {
			t.Fatal("webhook was rejected after queue capacity became available")
		}
	case <-time.After(time.Second):
		t.Fatal("webhook admission did not resume after queue capacity became available")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if service.HandleXrayWebhookContext(canceled, xraywebhook.Payload{}) {
		t.Fatal("canceled webhook admission was accepted")
	}

	<-service.operationGate
	gateReleased = true
}

func TestWebhookStopWakesAdmissionWaitingOnFullQueue(t *testing.T) {
	t.Parallel()

	service := newServiceWithBackend(NewState(), nil, nil, &fakeFirewall{})
	service.operationGate <- struct{}{}
	gateReleased := false
	t.Cleanup(func() {
		if !gateReleased {
			<-service.operationGate
		}
		_ = service.Close()
	})

	if !service.HandleXrayWebhook(xraywebhook.Payload{}) {
		t.Fatal("first webhook was not admitted")
	}
	deadline := time.Now().Add(time.Second)
	for len(service.webhookQueue) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker did not take first webhook")
		}
		time.Sleep(time.Millisecond)
	}
	for range cap(service.webhookQueue) {
		service.webhookQueue <- queuedWebhook{}
	}

	admissionDone := make(chan bool, 1)
	go func() {
		admissionDone <- service.HandleXrayWebhookContext(context.Background(), xraywebhook.Payload{})
	}()
	select {
	case <-admissionDone:
		t.Fatal("webhook admission returned before shutdown")
	case <-time.After(20 * time.Millisecond):
	}

	stopDone := make(chan struct{})
	go func() {
		service.signalWebhookStop()
		close(stopDone)
	}()
	select {
	case accepted := <-admissionDone:
		if accepted {
			t.Fatal("shutdown accepted a webhook waiting on full capacity")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not wake webhook admission")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("webhook stop remained blocked behind admission")
	}

	<-service.operationGate
	gateReleased = true
}
