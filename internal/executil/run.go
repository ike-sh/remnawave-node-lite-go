// Package executil runs short-lived external commands with bounded output.
package executil

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

const DefaultWaitDelay = 2 * time.Second

type Result struct {
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

func (r Result) DiagnosticOutput() []byte {
	if len(r.Stdout) == 0 {
		return append([]byte(nil), r.Stderr...)
	}
	if len(r.Stderr) == 0 {
		return append([]byte(nil), r.Stdout...)
	}
	combined := make([]byte, 0, len(r.Stdout)+1+len(r.Stderr))
	combined = append(combined, r.Stdout...)
	combined = append(combined, '\n')
	combined = append(combined, r.Stderr...)
	return combined
}

func (r Result) AnyTruncated() bool {
	return r.StdoutTruncated || r.StderrTruncated
}

// Run drains stdout and stderr while retaining at most maxOutputBytes for each
// stream. The writers report every input byte as consumed so discarded output
// cannot block the child process on a full pipe.
func Run(ctx context.Context, stdin io.Reader, maxOutputBytes int, name string, args ...string) (Result, error) {
	return run(ctx, stdin, maxOutputBytes, DefaultWaitDelay, name, args...)
}

func run(ctx context.Context, stdin io.Reader, maxOutputBytes int, waitDelay time.Duration, name string, args ...string) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxOutputBytes < 0 {
		maxOutputBytes = 0
	}

	stdout := &boundedBuffer{limit: maxOutputBytes}
	stderr := &boundedBuffer{limit: maxOutputBytes}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return Result{
		Stdout:          stdout.bytes(),
		Stderr:          stderr.bytes(),
		StdoutTruncated: stdout.wasTruncated(),
		StderrTruncated: stderr.wasTruncated(),
	}, err
}

type boundedBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	consumed := len(payload)
	remaining := b.limit - len(b.data)
	if remaining > len(payload) {
		remaining = len(payload)
	}
	if remaining > 0 {
		b.data = append(b.data, payload[:remaining]...)
	}
	if remaining < len(payload) {
		b.truncated = true
	}
	return consumed, nil
}

func (b *boundedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func (b *boundedBuffer) wasTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
