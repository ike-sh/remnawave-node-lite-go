//go:build linux

package netadmin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKillSocketsByIPPropagatesSSFailures(t *testing.T) {
	dir := t.TempDir()
	ssPath := filepath.Join(dir, "ss")
	if err := os.WriteFile(ssPath, []byte("#!/bin/sh\necho denied:$* >&2\nexit 23\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	err := KillSocketsByIP(context.Background(), "203.0.113.10")
	if err == nil {
		t.Fatal("non-zero ss exits must be returned")
	}
	message := err.Error()
	for _, expected := range []string{
		"ss -K src 203.0.113.10",
		"ss -K dst 203.0.113.10",
		"denied:-4 -K src 203.0.113.10",
		"denied:-4 -K dst 203.0.113.10",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("error %q does not contain %q", message, expected)
		}
	}
}

func TestKillSocketsByIPBracketsIPv6ForSS(t *testing.T) {
	dir := t.TempDir()
	ssPath := filepath.Join(dir, "ss")
	script := `#!/bin/sh
case "$*" in
  "-6 -K src [2001:db8::1]"|"-6 -K dst [2001:db8::1]") exit 0 ;;
  *) echo "unexpected:$*" >&2; exit 23 ;;
esac
`
	if err := os.WriteFile(ssPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if err := KillSocketsByIP(context.Background(), "2001:0db8::1"); err != nil {
		t.Fatalf("canonical IPv6 socket kill failed: %v", err)
	}
}

func TestKillSocketsByIPUnmapsIPv4MappedAddress(t *testing.T) {
	dir := t.TempDir()
	ssPath := filepath.Join(dir, "ss")
	script := `#!/bin/sh
case "$*" in
  "-4 -K src 203.0.113.10"|"-4 -K dst 203.0.113.10") exit 0 ;;
  *) echo "unexpected:$*" >&2; exit 23 ;;
esac
`
	if err := os.WriteFile(ssPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if err := KillSocketsByIP(context.Background(), "::ffff:203.0.113.10"); err != nil {
		t.Fatalf("IPv4-mapped socket kill failed: %v", err)
	}
}

func TestKillSocketsByIPRejectsInvalidInput(t *testing.T) {
	if err := KillSocketsByIP(context.Background(), "not-an-ip"); err == nil {
		t.Fatal("invalid IP must not be accepted")
	}
}
