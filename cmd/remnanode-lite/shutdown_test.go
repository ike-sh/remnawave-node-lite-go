package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNodeComponentCleanupKeepsPluginUntilCoreStops(t *testing.T) {
	managerStarted := make(chan struct{})
	coreStarted := make(chan struct{})
	releaseCore := make(chan struct{})
	pluginStarted := make(chan struct{})
	var networkStops atomic.Int32

	cleanup := &nodeComponentCleanup{
		stopNetwork: func() { networkStops.Add(1) },
		shutdownManager: func(context.Context) error {
			close(managerStarted)
			return nil
		},
		stopCore: func() error {
			close(coreStarted)
			<-releaseCore
			return nil
		},
		closePlugin: func(context.Context) error {
			close(pluginStarted)
			return nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- cleanup.Run(context.Background()) }()
	awaitCleanupSignal(t, managerStarted, "manager shutdown")
	awaitCleanupSignal(t, coreStarted, "core stop")
	select {
	case <-pluginStarted:
		t.Fatal("plugin cleanup started before core stopped")
	default:
	}
	close(releaseCore)
	awaitCleanupSignal(t, pluginStarted, "plugin cleanup")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := networkStops.Load(); got != 1 {
		t.Fatalf("network monitor stops = %d, want 1", got)
	}
	if err := cleanup.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := networkStops.Load(); got != 1 {
		t.Fatalf("repeated cleanup stopped network %d times", got)
	}
}

func TestNodeComponentCleanupPreservesPluginWhenCoreStopFails(t *testing.T) {
	stopErr := errors.New("stop failed")
	var pluginCalls atomic.Int32
	cleanup := &nodeComponentCleanup{
		stopCore: func() error { return stopErr },
		closePlugin: func(context.Context) error {
			pluginCalls.Add(1)
			return nil
		},
	}

	err := cleanup.Run(context.Background())
	if !errors.Is(err, stopErr) || !strings.Contains(err.Error(), "stop rw-core") {
		t.Fatalf("cleanup error = %v", err)
	}
	if got := pluginCalls.Load(); got != 0 {
		t.Fatalf("plugin cleanup calls = %d, want 0", got)
	}
}

func TestNodeComponentCleanupRetriesTransientCoreFailureBeforePlugin(t *testing.T) {
	transientErr := errors.New("transient stop failure")
	var coreCalls atomic.Int32
	var pluginCalls atomic.Int32
	cleanup := &nodeComponentCleanup{
		stopCore: func() error {
			if coreCalls.Add(1) == 1 {
				return transientErr
			}
			return nil
		},
		closePlugin: func(context.Context) error {
			pluginCalls.Add(1)
			return nil
		},
	}

	if err := cleanup.Run(context.Background()); err != nil {
		t.Fatalf("cleanup after transient core failure: %v", err)
	}
	if got := coreCalls.Load(); got != 2 {
		t.Fatalf("core stop calls = %d, want 2", got)
	}
	if got := pluginCalls.Load(); got != 1 {
		t.Fatalf("plugin close calls = %d, want 1", got)
	}
}

func TestNodeComponentCleanupRetriesTransientPluginFailure(t *testing.T) {
	transientErr := errors.New("transient nft failure")
	var pluginCalls atomic.Int32
	cleanup := &nodeComponentCleanup{
		stopCore: func() error { return nil },
		closePlugin: func(context.Context) error {
			if pluginCalls.Add(1) == 1 {
				return transientErr
			}
			return nil
		},
	}

	if err := cleanup.Run(context.Background()); err != nil {
		t.Fatalf("cleanup after transient plugin failure: %v", err)
	}
	if got := pluginCalls.Load(); got != 2 {
		t.Fatalf("plugin close calls = %d, want 2", got)
	}
}

func TestNodeComponentCleanupHonorsSharedDeadline(t *testing.T) {
	release := make(chan struct{})
	cleanup := &nodeComponentCleanup{
		shutdownManager: func(context.Context) error {
			<-release
			return nil
		},
		stopCore: func() error {
			<-release
			return nil
		},
	}
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := cleanup.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("cleanup exceeded shared deadline: %s", elapsed)
	}
}

func awaitCleanupSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
