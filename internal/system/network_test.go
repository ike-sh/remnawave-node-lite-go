package system

import (
	"sync"
	"testing"
	"time"
)

func TestNetworkMonitorReplacesSamplesAndClearsMissingInterface(t *testing.T) {
	t.Parallel()

	started := time.Unix(100, 0)
	monitor := &NetworkMonitor{
		defaultIface: "eth0",
		previous: map[string]interfaceSample{
			"eth0":  {rxBytes: 100, txBytes: 200, timestamp: started},
			"stale": {rxBytes: 1, txBytes: 1, timestamp: started},
		},
	}
	monitor.updateSamples(map[string]interfaceSample{
		"eth0": {rxBytes: 300, txBytes: 500},
	}, started.Add(2*time.Second))

	if len(monitor.previous) != 1 {
		t.Fatalf("previous samples = %d, want 1", len(monitor.previous))
	}
	if _, exists := monitor.previous["stale"]; exists {
		t.Fatal("removed interface remained in previous samples")
	}
	if monitor.current == nil || monitor.current.RxBytesPerSec != 100 || monitor.current.TxBytesPerSec != 150 {
		t.Fatalf("current sample = %#v", monitor.current)
	}

	monitor.updateSamples(map[string]interfaceSample{}, started.Add(3*time.Second))
	if monitor.current != nil || len(monitor.previous) != 0 {
		t.Fatalf("missing interface left stale state: current=%#v previous=%d", monitor.current, len(monitor.previous))
	}
}

func TestNetworkMonitorFollowsDefaultInterfaceChanges(t *testing.T) {
	t.Parallel()

	started := time.Unix(100, 0)
	monitor := &NetworkMonitor{
		defaultIface: "eth0",
		previous: map[string]interfaceSample{
			"eth0": {rxBytes: 100, txBytes: 100, timestamp: started},
			"ens3": {rxBytes: 200, txBytes: 300, timestamp: started},
		},
	}
	monitor.updateSamplesForInterface(map[string]interfaceSample{
		"eth0": {rxBytes: 150, txBytes: 150},
		"ens3": {rxBytes: 400, txBytes: 700},
	}, started.Add(2*time.Second), "ens3")

	if monitor.current == nil || monitor.current.Interface != "ens3" ||
		monitor.current.RxBytesPerSec != 100 || monitor.current.TxBytesPerSec != 200 {
		t.Fatalf("current sample after route change = %#v", monitor.current)
	}
	if monitor.defaultIface != "ens3" {
		t.Fatalf("default interface = %q, want ens3", monitor.defaultIface)
	}
}

func TestNetworkMonitorStopIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()

	monitor := &NetworkMonitor{stop: make(chan struct{})}
	var callers sync.WaitGroup
	for range 8 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			monitor.Stop()
		}()
	}
	callers.Wait()
	select {
	case <-monitor.stop:
	default:
		t.Fatal("Stop did not close the channel")
	}
}

func TestZeroValueNetworkMonitorStopDoesNotPanic(t *testing.T) {
	t.Parallel()
	var monitor NetworkMonitor
	monitor.Stop()
	monitor.Stop()
}
