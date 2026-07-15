package xtls

import (
	"context"
	"time"
)

const defaultRPCTimeout = 5 * time.Second

func withRPCTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return withRPCDeadline(parent, defaultRPCTimeout)
}

func withRPCDeadline(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}
