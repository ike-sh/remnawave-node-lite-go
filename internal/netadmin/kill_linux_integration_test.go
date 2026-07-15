//go:build linux

package netadmin

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	socketKillIntegrationEnv = "REMNANODE_SOCKET_KILL_INTEGRATION"
	socketKillNetNSChildEnv  = "REMNANODE_SOCKET_KILL_NETNS_CHILD"
)

func TestKillSocketsInNetworkNamespace(t *testing.T) {
	if os.Getenv(socketKillIntegrationEnv) != "1" {
		t.Skip("set REMNANODE_SOCKET_KILL_INTEGRATION=1 to run the isolated socket-kill test")
	}
	for _, executable := range []string{"ip", "ss"} {
		if _, err := exec.LookPath(executable); err != nil {
			t.Fatalf("%s executable is required: %v", executable, err)
		}
	}
	if os.Getenv(socketKillNetNSChildEnv) != "1" {
		runSocketKillIntegrationChild(t)
		return
	}

	runIP(t, "link", "set", "lo", "up")
	tests := []struct {
		name          string
		network       string
		listenAddress string
		localAddress  string
		prefix        string
	}{
		{name: "ipv4", network: "tcp4", listenAddress: "127.0.0.1:0", localAddress: "198.51.100.1", prefix: "198.51.100.1/32"},
		{name: "ipv6", network: "tcp6", listenAddress: "[::1]:0", localAddress: "2001:db8::1", prefix: "2001:db8::1/128"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runIP(t, "address", "add", test.prefix, "dev", "lo")
			t.Cleanup(func() {
				runIP(t, "address", "delete", test.prefix, "dev", "lo")
			})
			runSocketKillCase(t, test.network, test.listenAddress, test.localAddress)
		})
	}
}

func runSocketKillCase(t *testing.T, network, listenAddress, localAddress string) {
	t.Helper()

	listener, err := net.Listen(network, listenAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	dialer := net.Dialer{
		Timeout:   time.Second,
		LocalAddr: &net.TCPAddr{IP: net.ParseIP(localAddress)},
	}
	client, err := dialer.Dial(network, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var server net.Conn
	select {
	case server = <-accepted:
		defer server.Close()
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("timed out accepting test connection")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := KillSocketsByIP(ctx, localAddress); err != nil {
		t.Fatalf("KillSocketsByIP: %v", err)
	}

	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	_, err = server.Read(buffer)
	if err == nil {
		t.Fatal("connection remained readable after socket kill")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("connection was not destroyed before deadline: %v", err)
	}
}

func runSocketKillIntegrationChild(t *testing.T) {
	t.Helper()
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		t.Fatalf("unshare executable is required: %v", err)
	}
	args := []string{"--net"}
	if os.Geteuid() != 0 {
		args = append([]string{"--user", "--map-root-user"}, args...)
	}
	args = append(args, os.Args[0], "-test.run=^TestKillSocketsInNetworkNamespace$", "-test.count=1", "-test.v")
	command := exec.Command(unshare, args...)
	command.Env = append(os.Environ(), socketKillNetNSChildEnv+"=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated socket-kill test failed: %v\n%s", err, output)
	}
	t.Logf("isolated socket-kill test output:\n%s", output)
}

func runIP(t *testing.T, args ...string) {
	t.Helper()
	output, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ip %v: %v\n%s", args, err, output)
	}
}
