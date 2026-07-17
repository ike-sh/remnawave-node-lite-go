//go:build linux

package netadmin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	socketKillIntegrationEnv = "REMNANODE_SOCKET_KILL_INTEGRATION"
	socketKillNetNSChildEnv  = "REMNANODE_SOCKET_KILL_NETNS_CHILD"
)

var errIPv4DumpBlockedForMappedTest = errors.New("IPv4 dump blocked after mapped IPv6 coverage")

type blockIPv4DumpTransport struct {
	sockDiagTransport
}

func (t blockIPv4DumpTransport) send(ctx context.Context, message []byte) error {
	header, err := decodeNetlinkHeader(message)
	if err != nil {
		return err
	}
	if header.message == unix.SOCK_DIAG_BY_FAMILY &&
		len(message) >= unix.NLMSG_HDRLEN+inetDiagRequestPayloadLen &&
		message[unix.NLMSG_HDRLEN] == unix.AF_INET {
		return errIPv4DumpBlockedForMappedTest
	}
	return t.sockDiagTransport.send(ctx, message)
}

func TestKillSocketsInNetworkNamespace(t *testing.T) {
	if os.Getenv(socketKillIntegrationEnv) != "1" {
		t.Skip("set REMNANODE_SOCKET_KILL_INTEGRATION=1 to run the isolated socket-kill test")
	}
	for _, executable := range []string{"ip"} {
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
	t.Run("dual-stack-ipv4-mapped", func(t *testing.T) {
		const (
			localAddress = "198.51.100.2"
			prefix       = localAddress + "/32"
		)
		runIP(t, "address", "add", prefix, "dev", "lo")
		t.Cleanup(func() {
			runIP(t, "address", "delete", prefix, "dev", "lo")
		})

		listenConfig := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
			var socketErr error
			if err := raw.Control(func(fd uintptr) {
				socketErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_V6ONLY, 0)
			}); err != nil {
				return err
			}
			return socketErr
		}}
		listener, err := listenConfig.Listen(context.Background(), "tcp6", "[::]:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		port := listener.Addr().(*net.TCPAddr).Port
		runSocketKillWithListener(
			t,
			listener,
			"tcp4",
			net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
			localAddress,
			unix.AF_INET6,
		)
	})
}

func runSocketKillCase(t *testing.T, network, listenAddress, localAddress string) {
	t.Helper()

	listener, err := net.Listen(network, listenAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	runSocketKillWithListener(t, listener, network, listener.Addr().String(), localAddress, 0)
}

func runSocketKillWithListener(
	t *testing.T,
	listener net.Listener,
	dialNetwork string,
	dialAddress string,
	localAddress string,
	expectedServerDomain int,
) {
	t.Helper()

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
	client, err := dialer.Dial(dialNetwork, dialAddress)
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
	if expectedServerDomain != 0 {
		domain, err := connectionSocketDomain(server)
		if err != nil {
			t.Fatal(err)
		}
		if domain != expectedServerDomain {
			t.Fatalf("accepted socket domain = %d, want %d", domain, expectedServerDomain)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if expectedServerDomain == unix.AF_INET6 {
		factory := func() (sockDiagTransport, error) {
			transport, err := openSockDiagTransport()
			if err != nil {
				return nil, err
			}
			return blockIPv4DumpTransport{sockDiagTransport: transport}, nil
		}
		err := killSocketsByAddr(ctx, netip.MustParseAddr(localAddress), factory)
		if !errors.Is(err, errIPv4DumpBlockedForMappedTest) {
			t.Fatalf("mapped-only socket kill error = %v, want IPv4 dump sentinel", err)
		}
	} else if err := KillSocketsByIP(ctx, localAddress); err != nil {
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
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer retryCancel()
	if err := KillSocketsByIP(retryCtx, localAddress); err != nil {
		t.Fatalf("idempotent KillSocketsByIP retry: %v", err)
	}
}

func connectionSocketDomain(connection net.Conn) (int, error) {
	syscallConnection, ok := connection.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("connection %T does not expose syscall.Conn", connection)
	}
	raw, err := syscallConnection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var domain int
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		domain, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_DOMAIN)
	}); err != nil {
		return 0, err
	}
	return domain, socketErr
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
