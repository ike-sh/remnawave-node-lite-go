//go:build linux

package netadmin

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestKillSocketsByIPNoMatchingSocket(t *testing.T) {
	requireSocketIntegration(t)
	for _, ip := range []string{"192.0.2.123", "2001:db8::123"} {
		if err := KillSocketsByIP(ip); err != nil {
			t.Fatalf("no matching socket for %s: %v", ip, err)
		}
	}
	if err := KillSocketsByIP("invalid"); err == nil {
		t.Fatal("invalid IP accepted")
	}
}

func TestKillSocketsByIPClosesEstablishedTCPBothFamilies(t *testing.T) {
	requireSocketIntegration(t)
	if supported, reason := probeSocketDestroy(t); !supported {
		t.Skipf("SKIP — host kernel does not support successful SOCK_DESTROY: %s", reason)
	}
	for _, tc := range []struct{ network, ip string }{{"tcp4", "127.0.0.1"}, {"tcp6", "::1"}} {
		t.Run(tc.network, func(t *testing.T) {
			server, client := establishedPair(t, tc.network, tc.ip)
			defer server.Close()
			defer client.Close()
			if err := KillSocketsByIP(tc.ip); err != nil {
				t.Fatal(err)
			}
			assertDisconnected(t, server)
			assertDisconnected(t, client)
			if _, err := client.Write([]byte("x")); err == nil {
				t.Fatal("client write succeeded after socket destroy")
			}
		})
	}
}

// Probe with iproute2 itself before testing our wrapper. A failed independent
// probe is an environment limitation; a wrapper failure after a successful
// probe is a real implementation failure.
func probeSocketDestroy(t *testing.T) (bool, string) {
	t.Helper()
	server, client := establishedPair(t, "tcp4", "127.0.0.1")
	defer server.Close()
	defer client.Close()
	output, err := exec.Command("ss", "-4", "-t", "-K", "src", "127.0.0.1").CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(output))
		if strings.Contains(lower, "not supported") || strings.Contains(lower, "permission denied") {
			return false, strings.TrimSpace(string(output))
		}
		t.Fatalf("independent ss -K capability probe failed: %v: %s", err, output)
	}
	remaining, err := exec.Command("ss", "-4", "-H", "-tn", "src", "127.0.0.1").CombinedOutput()
	if err != nil {
		t.Fatalf("verify independent ss -K probe: %v: %s", err, remaining)
	}
	if strings.TrimSpace(string(remaining)) != "" {
		return false, "ss -K returned success but left established TCP sockets"
	}
	return true, ""
}

func TestKillSocketsByIPReportsSilentUnsupportedKernel(t *testing.T) {
	requireSocketIntegration(t)
	if !wslKernel() {
		t.Skip("negative capability check applies to the Docker Desktop WSL2 kernel")
	}
	for _, tc := range []struct{ network, ip string }{{"tcp4", "127.0.0.1"}, {"tcp6", "::1"}} {
		t.Run(tc.network, func(t *testing.T) {
			server, client := establishedPair(t, tc.network, tc.ip)
			defer server.Close()
			defer client.Close()
			if err := KillSocketsByIP(tc.ip); err == nil || !strings.Contains(err.Error(), "TCP sockets remain") {
				t.Fatalf("silent kernel failure not detected: %v", err)
			}
		})
	}
}

func requireSocketIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("REMNANODE_SOCKET_INTEGRATION") != "1" {
		t.Skip("set REMNANODE_SOCKET_INTEGRATION=1 in an isolated Linux network namespace")
	}
	if !HasCapNetAdmin() {
		t.Skip("SKIP — CAP_NET_ADMIN unavailable")
	}
	if _, err := exec.LookPath("ss"); err != nil {
		t.Skip("SKIP — ss executable unavailable")
	}
}

func wslKernel() bool {
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft-standard-wsl2")
}

func establishedPair(t *testing.T, network, ip string) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen(network, net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("SKIP — %s loopback unavailable: %v", network, err)
	}
	defer listener.Close()
	client, err := net.Dial(network, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	family, filter := "-4", ip
	if network == "tcp6" {
		family, filter = "-6", "["+ip+"]"
	}
	matched, err := exec.Command("ss", family, "-H", "-tn", "src", filter).CombinedOutput()
	if err != nil || strings.TrimSpace(string(matched)) == "" {
		server.Close()
		client.Close()
		t.Fatalf("TCP pair not ESTABLISHED: %v, %s", err, matched)
	}
	return server, client
}

func assertDisconnected(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("matching TCP socket remains readable")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("matching TCP socket remains ESTABLISHED")
	}
}
