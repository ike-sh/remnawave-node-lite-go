package httpserver

import (
	"context"
	"testing"
	"time"
)

func TestXrayLifecycleGateSharesStartsAndExcludesMutations(t *testing.T) {
	t.Parallel()

	var gate xrayLifecycleGate
	if !gate.acquireStart(context.Background()) || !gate.acquireStart(context.Background()) {
		t.Fatal("failed to acquire shared start leases")
	}
	if mode, starts, _ := gate.snapshot(); mode != xrayLifecycleStarts || starts != 2 {
		t.Fatalf("gate state = mode %d, starts %d; want starts/2", mode, starts)
	}

	exclusive := make(chan bool, 1)
	go func() { exclusive <- gate.acquireExclusive(context.Background()) }()
	awaitExclusiveWaiter(t, &gate)
	select {
	case <-exclusive:
		t.Fatal("exclusive lease entered while starts were active")
	default:
	}

	gate.releaseStart()
	select {
	case <-exclusive:
		t.Fatal("exclusive lease entered before every start was released")
	default:
	}

	gate.releaseStart()
	if !<-exclusive {
		t.Fatal("exclusive lease was not acquired after starts completed")
	}
	gate.releaseExclusive()
}

func TestXrayLifecycleGatePrioritizesWaitingExclusiveLease(t *testing.T) {
	t.Parallel()

	var gate xrayLifecycleGate
	if !gate.acquireStart(context.Background()) {
		t.Fatal("failed to acquire first start lease")
	}
	exclusive := make(chan bool, 1)
	go func() { exclusive <- gate.acquireExclusive(context.Background()) }()
	awaitExclusiveWaiter(t, &gate)

	observed := make(chan struct{})
	secondStart := make(chan bool, 1)
	go func() {
		secondStart <- gate.acquireStart(&observedDoneContext{Context: context.Background(), observed: observed})
	}()
	awaitTestSignal(t, observed, "start waiting behind exclusive lease")
	select {
	case <-secondStart:
		t.Fatal("new start bypassed a waiting exclusive lease")
	default:
	}

	gate.releaseStart()
	if !<-exclusive {
		t.Fatal("exclusive waiter was not admitted")
	}
	select {
	case <-secondStart:
		t.Fatal("start entered while the exclusive lease was active")
	default:
	}
	gate.releaseExclusive()
	if !<-secondStart {
		t.Fatal("start was not admitted after the exclusive lease")
	}
	gate.releaseStart()
}

func TestXrayLifecycleGateWaitCanBeCanceled(t *testing.T) {
	t.Parallel()

	var gate xrayLifecycleGate
	if !gate.acquireExclusive(context.Background()) {
		t.Fatal("failed to acquire exclusive lease")
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan bool, 1)
	go func() { waiting <- gate.acquireStart(ctx) }()
	cancel()
	if <-waiting {
		t.Fatal("canceled start waiter acquired a lease")
	}
	gate.releaseExclusive()

	if !gate.acquireStart(context.Background()) {
		t.Fatal("gate remained unavailable after canceled waiter")
	}
	gate.releaseStart()
}

func awaitExclusiveWaiter(t *testing.T, gate *xrayLifecycleGate) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, _, waiting := gate.snapshot(); waiting != 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for exclusive lifecycle waiter")
}
