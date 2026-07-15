package xtls

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestWithRPCTimeoutAddsBoundedDeadline(t *testing.T) {
	t.Parallel()

	started := time.Now()
	ctx, cancel := withRPCTimeout(nil)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("default RPC context has no deadline")
	}
	remaining := deadline.Sub(started)
	if remaining < defaultRPCTimeout-time.Second || remaining > defaultRPCTimeout+time.Second {
		t.Fatalf("deadline remaining = %v, want approximately %v", remaining, defaultRPCTimeout)
	}
}

func TestWithRPCTimeoutKeepsEarlierParentDeadline(t *testing.T) {
	t.Parallel()

	parent, parentCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer parentCancel()
	parentDeadline, _ := parent.Deadline()
	ctx, cancel := withRPCTimeout(parent)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(parentDeadline) {
		t.Fatalf("deadline = %v, want parent deadline %v", deadline, parentDeadline)
	}
}

func TestHandlerRPCAddsDeadlineAndPropagatesCancellation(t *testing.T) {
	t.Parallel()

	var sawDeadline bool
	conn := &fakeInvokeConn{invoke: func(ctx context.Context, method string, _, _ any, _ ...grpc.CallOption) error {
		if method != handlerAlterInboundMethod {
			t.Fatalf("method = %q", method)
		}
		_, sawDeadline = ctx.Deadline()
		return ctx.Err()
	}}
	api := &HandlerAPI{conn: conn}
	if result := api.AddVlessUser(nil, "in-1", "u1", "00000000-0000-4000-8000-000000000001", "", 0); !result.OK {
		t.Fatalf("result = %+v, want success", result)
	}
	if !sawDeadline {
		t.Fatal("handler client did not receive a deadline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := api.RemoveUser(ctx, "in-1", "u1")
	if result.OK || result.Message != context.Canceled.Error() {
		t.Fatalf("result = %+v, want propagated cancellation", result)
	}
}

func TestStatsRPCAddsDeadlineAndPropagatesCancellation(t *testing.T) {
	t.Parallel()

	var sawDeadline bool
	conn := &fakeInvokeConn{invoke: func(ctx context.Context, method string, _, _ any, _ ...grpc.CallOption) error {
		if method != statsGetSysStatsMethod {
			t.Fatalf("method = %q", method)
		}
		_, sawDeadline = ctx.Deadline()
		return ctx.Err()
	}}
	api := NewStatsAPI(conn, nil)
	if _, err := api.GetSysStats(nil); err != nil {
		t.Fatal(err)
	}
	if !sawDeadline {
		t.Fatal("stats client did not receive a deadline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := api.GetSysStats(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
