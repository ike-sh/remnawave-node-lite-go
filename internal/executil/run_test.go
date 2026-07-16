package executil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const commandHelperEnv = "GO_WANT_EXECUTIL_HELPER"

func TestCommandHelper(t *testing.T) {
	mode := os.Getenv(commandHelperEnv)
	if mode == "" {
		return
	}
	switch mode {
	case "large-output":
		fmt.Fprint(os.Stdout, strings.Repeat("o", 32<<10))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 32<<10))
	case "sleep":
		time.Sleep(5 * time.Second)
	case "pipe-parent":
		child := exec.Command(os.Args[0], "-test.run=^TestCommandHelper$", "--")
		child.Env = append(os.Environ(), commandHelperEnv+"=pipe-child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
	case "pipe-child":
		time.Sleep(2 * time.Second)
	default:
		os.Exit(4)
	}
	os.Exit(0)
}

func TestRunBoundsAndDrainsCombinedOutput(t *testing.T) {
	result, err := runHelper(context.Background(), 1024, "large-output")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) != 1024 || len(result.Stderr) != 1024 ||
		!result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("stdout=%d/%v stderr=%d/%v", len(result.Stdout), result.StdoutTruncated, len(result.Stderr), result.StderrTruncated)
	}
	if len(result.DiagnosticOutput()) != 2049 || !result.AnyTruncated() {
		t.Fatalf("combined diagnostic result is not bounded per stream")
	}
}

func TestRunHonorsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runHelper(ctx, 1024, "sleep")
	if err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("Run error=%v context=%v", err, ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timed command returned after %s", elapsed)
	}
}

func TestRunWaitDelayBoundsInheritedOutputPipe(t *testing.T) {
	started := time.Now()
	_, err := runHelperWithWaitDelay(context.Background(), 1024, 50*time.Millisecond, "pipe-parent")
	if err == nil {
		t.Fatal("inherited pipe timeout unexpectedly reported success")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("inherited pipe remained open for %s", elapsed)
	}
}

func runHelper(ctx context.Context, limit int, mode string) (Result, error) {
	return runHelperWithWaitDelay(ctx, limit, DefaultWaitDelay, mode)
}

func runHelperWithWaitDelay(ctx context.Context, limit int, waitDelay time.Duration, mode string) (Result, error) {
	previous, present := os.LookupEnv(commandHelperEnv)
	previousRace, racePresent := os.LookupEnv("GORACE")
	_ = os.Setenv(commandHelperEnv, mode)
	_ = os.Setenv("GORACE", "atexit_sleep_ms=0")
	defer func() {
		if present {
			_ = os.Setenv(commandHelperEnv, previous)
		} else {
			_ = os.Unsetenv(commandHelperEnv)
		}
		if racePresent {
			_ = os.Setenv("GORACE", previousRace)
		} else {
			_ = os.Unsetenv("GORACE")
		}
	}()
	return run(ctx, nil, limit, waitDelay, os.Args[0], "-test.run=^TestCommandHelper$", "--")
}
