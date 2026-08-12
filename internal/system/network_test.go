package system

import (
	"sync"
	"testing"
)

func TestNetworkMonitorStopIsConcurrentSafe(t *testing.T) {
	monitor := NewNetworkMonitor()
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			monitor.Stop()
		}()
	}
	wg.Wait()
}
