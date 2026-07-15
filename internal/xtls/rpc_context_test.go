package xtls

import (
	"context"
	"errors"
	"testing"
	"time"

	proxcommand "github.com/xtls/xray-core/app/proxyman/command"
	statscommand "github.com/xtls/xray-core/app/stats/command"
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

type deadlineHandlerClient struct {
	proxcommand.HandlerServiceClient
	sawDeadline bool
}

func (c *deadlineHandlerClient) AlterInbound(ctx context.Context, _ *proxcommand.AlterInboundRequest, _ ...grpc.CallOption) (*proxcommand.AlterInboundResponse, error) {
	_, c.sawDeadline = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &proxcommand.AlterInboundResponse{}, nil
}

func TestHandlerRPCAddsDeadlineAndPropagatesCancellation(t *testing.T) {
	t.Parallel()

	client := &deadlineHandlerClient{}
	api := &HandlerAPI{client: client}
	if result := api.AddVlessUser(nil, "in-1", "u1", "00000000-0000-4000-8000-000000000001", "", 0); !result.OK {
		t.Fatalf("result = %+v, want success", result)
	}
	if !client.sawDeadline {
		t.Fatal("handler client did not receive a deadline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := api.RemoveUser(ctx, "in-1", "u1")
	if result.OK || result.Message != context.Canceled.Error() {
		t.Fatalf("result = %+v, want propagated cancellation", result)
	}
}

type deadlineStatsClient struct {
	statscommand.StatsServiceClient
	sawDeadline bool
}

func (c *deadlineStatsClient) GetSysStats(ctx context.Context, _ *statscommand.SysStatsRequest, _ ...grpc.CallOption) (*statscommand.SysStatsResponse, error) {
	_, c.sawDeadline = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &statscommand.SysStatsResponse{}, nil
}

func TestStatsRPCAddsDeadlineAndPropagatesCancellation(t *testing.T) {
	t.Parallel()

	client := &deadlineStatsClient{}
	api := &StatsAPI{client: client, capabilities: &StatsCapabilities{}}
	if _, err := api.GetSysStats(nil); err != nil {
		t.Fatal(err)
	}
	if !client.sawDeadline {
		t.Fatal("stats client did not receive a deadline")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := api.GetSysStats(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
