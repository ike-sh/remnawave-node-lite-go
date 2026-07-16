package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const componentCleanupRetryDelay = 100 * time.Millisecond

type nodeComponentCleanup struct {
	once sync.Once

	stopNetwork     func()
	shutdownManager func(context.Context) error
	stopCore        func() error
	closePlugin     func(context.Context) error

	err error
}

type componentCleanupResult struct {
	action string
	err    error
}

func (c *nodeComponentCleanup) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.once.Do(func() {
		c.err = c.run(ctx)
	})
	return c.err
}

func (c *nodeComponentCleanup) run(ctx context.Context) error {
	if c.stopNetwork != nil {
		c.stopNetwork()
	}
	results := make(chan componentCleanupResult, 2)

	// Version recovery is independent of core ownership and can stop in
	// parallel with the ordered core -> firewall shutdown below.
	go func() {
		var err error
		if c.shutdownManager != nil {
			err = retryComponentCleanup(ctx, func() error { return c.shutdownManager(ctx) })
		}
		results <- componentCleanupResult{action: "shutdown Xray manager", err: err}
	}()
	go func() {
		if c.stopCore != nil {
			if err := retryComponentCleanup(ctx, c.stopCore); err != nil {
				results <- componentCleanupResult{action: "stop rw-core", err: err}
				return
			}
		}
		// Keep nft filtering active until rw-core is confirmed stopped.
		var err error
		if c.closePlugin != nil {
			err = retryComponentCleanup(ctx, func() error { return c.closePlugin(ctx) })
		}
		results <- componentCleanupResult{action: "close plugin service after stopping rw-core", err: err}
	}()

	var cleanupErr error
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", result.action, result.err))
			}
		case <-ctx.Done():
			return errors.Join(cleanupErr, fmt.Errorf("shutdown components: %w", ctx.Err()))
		}
	}
	return cleanupErr
}

func retryComponentCleanup(ctx context.Context, action func() error) error {
	firstErr := action()
	if firstErr == nil {
		return nil
	}
	timer := time.NewTimer(componentCleanupRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.Join(firstErr, ctx.Err())
	case <-timer.C:
	}
	if secondErr := action(); secondErr != nil {
		return errors.Join(firstErr, secondErr)
	}
	return nil
}
