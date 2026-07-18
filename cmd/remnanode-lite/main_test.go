package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRunCLIStartsDaemonOnlyWithoutArguments(t *testing.T) {
	var daemonCalls int
	code := runCLI(nil, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, func() error {
		daemonCalls++
		return nil
	}, func([]string) int {
		t.Fatal("doctor called")
		return 1
	})

	if code != 0 || daemonCalls != 1 {
		t.Fatalf("runCLI() = %d, daemon calls = %d", code, daemonCalls)
	}
}

func TestRunCLIReportsDaemonFailure(t *testing.T) {
	var stderr bytes.Buffer
	code := runCLI(nil, strings.NewReader(""), &bytes.Buffer{}, &stderr, func() error {
		return errors.New("startup failed")
	}, func([]string) int { return 0 })

	if code != 1 || !strings.Contains(stderr.String(), "startup failed") {
		t.Fatalf("runCLI() = %d, stderr = %q", code, stderr.String())
	}
}

func TestRunCLIRejectsUnknownMalformedAndExtraArguments(t *testing.T) {
	tests := [][]string{
		{"help", "extra"},
		{"version", "extra"},
		{"--version", "extra"},
		{"doctor", "--unknown"},
		{"doctor", "--env"},
		{"doctor", "--env", ""},
		{"doctor", "--env", "/tmp/node.env", "extra"},
		{"validate-secret", "extra"},
		{"canonicalize-secret"},
		{"canonicalize-secret", "-", "extra"},
		{"release-url", "v0.1.0"},
		{"release-url", "v0.1.0", "amd64", "extra"},
		{"install-script-url", "v0.1.0"},
		{"install-script-url", "v0.1.0", "install-node.sh", "extra"},
	}

	for _, args := range tests {
		args := args
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stderr bytes.Buffer
			daemonCalls := 0
			doctorCalls := 0
			code := runCLI(args, strings.NewReader(""), &bytes.Buffer{}, &stderr, func() error {
				daemonCalls++
				return nil
			}, func([]string) int {
				doctorCalls++
				return 0
			})

			if code != 2 {
				t.Fatalf("runCLI(%q) = %d, want 2", args, code)
			}
			if !strings.Contains(stderr.String(), "usage:") {
				t.Fatalf("stderr = %q, want usage", stderr.String())
			}
			if daemonCalls != 0 || doctorCalls != 0 {
				t.Fatalf("daemon calls = %d, doctor calls = %d", daemonCalls, doctorCalls)
			}
		})
	}
}

func TestRunCLIRejectsUnknownCommandWithoutStartingDaemon(t *testing.T) {
	var stderr bytes.Buffer
	daemonCalls := 0
	code := runCLI([]string{"unknown"}, strings.NewReader(""), &bytes.Buffer{}, &stderr, func() error {
		daemonCalls++
		return nil
	}, func([]string) int {
		t.Fatal("doctor called")
		return 0
	})

	if code != 1 || daemonCalls != 0 {
		t.Fatalf("runCLI() = %d, daemon calls = %d", code, daemonCalls)
	}
	if !strings.Contains(stderr.String(), "Unknown command: unknown") || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCLIPreservesCommandDispatch(t *testing.T) {
	t.Run("help aliases", func(t *testing.T) {
		for _, command := range []string{"help", "-h", "--help"} {
			var stdout bytes.Buffer
			code := runCLI([]string{command}, strings.NewReader(""), &stdout, &bytes.Buffer{}, func() error {
				t.Fatal("daemon called")
				return nil
			}, func([]string) int { return 0 })
			if code != 0 || !strings.Contains(stdout.String(), "usage:") {
				t.Fatalf("%s: code = %d, stdout = %q", command, code, stdout.String())
			}
		}
	})

	t.Run("version aliases", func(t *testing.T) {
		for _, command := range []string{"version", "-version", "--version"} {
			var stdout bytes.Buffer
			code := runCLI([]string{command}, strings.NewReader(""), &stdout, &bytes.Buffer{}, func() error {
				t.Fatal("daemon called")
				return nil
			}, func([]string) int { return 0 })
			if code != 0 || !strings.Contains(stdout.String(), "remnawave-node-lite-go") {
				t.Fatalf("%s: code = %d, stdout = %q", command, code, stdout.String())
			}
		}
	})

	t.Run("doctor env", func(t *testing.T) {
		var gotArgs []string
		code := runCLI([]string{"doctor", "--env", "/tmp/node.env"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, func() error {
			t.Fatal("daemon called")
			return nil
		}, func(args []string) int {
			gotArgs = append([]string(nil), args...)
			return 7
		})
		if code != 7 || strings.Join(gotArgs, " ") != "--env /tmp/node.env" {
			t.Fatalf("code = %d, doctor args = %q", code, gotArgs)
		}
	})

	t.Run("release URLs", func(t *testing.T) {
		for _, test := range []struct {
			args []string
			want string
		}{
			{[]string{"release-url", "v0.1.0", "amd64"}, "/v0.1.0/remnanode-lite_linux_amd64.tar.gz"},
			{[]string{"install-script-url", "v0.1.0", "install-node.sh"}, "/v0.1.0/scripts/install-node.sh"},
		} {
			var stdout bytes.Buffer
			code := runCLI(test.args, strings.NewReader(""), &stdout, &bytes.Buffer{}, func() error {
				t.Fatal("daemon called")
				return nil
			}, func([]string) int { return 0 })
			if code != 0 || !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("runCLI(%q) = %d, stdout = %q", test.args, code, stdout.String())
			}
		}
	})
}
