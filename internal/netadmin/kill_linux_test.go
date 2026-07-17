//go:build linux

package netadmin

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type fakeSockDiagTransport struct {
	sent       [][]byte
	packets    []netlinkPacket
	portID     uint32
	receiveFn  func(context.Context) (netlinkPacket, error)
	sendErr    error
	receiveErr error
	closed     bool
}

func (t *fakeSockDiagTransport) localPortID() uint32 { return t.portID }

func (t *fakeSockDiagTransport) send(ctx context.Context, message []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.sendErr != nil {
		return t.sendErr
	}
	t.sent = append(t.sent, append([]byte(nil), message...))
	return nil
}

func (t *fakeSockDiagTransport) receive(ctx context.Context) (netlinkPacket, error) {
	if t.receiveFn != nil {
		return t.receiveFn(ctx)
	}
	if err := ctx.Err(); err != nil {
		return netlinkPacket{}, err
	}
	if t.receiveErr != nil {
		return netlinkPacket{}, t.receiveErr
	}
	if len(t.packets) == 0 {
		return netlinkPacket{}, errors.New("unexpected fake netlink receive")
	}
	packet := t.packets[0]
	t.packets = t.packets[1:]
	return packet, nil
}

func (t *fakeSockDiagTransport) close() error {
	t.closed = true
	return nil
}

func fakeSockDiagFactory(transports ...*fakeSockDiagTransport) sockDiagTransportFactory {
	next := 0
	return func() (sockDiagTransport, error) {
		if next >= len(transports) {
			return nil, errors.New("unexpected fake transport open")
		}
		transport := transports[next]
		next++
		return transport, nil
	}
}

func TestKillSocketsByAddrsDestroysNativeAndMappedEndpointsInOneDump(t *testing.T) {
	t.Parallel()

	targets := []netip.Addr{
		netip.MustParseAddr("::ffff:203.0.113.10"),
		netip.MustParseAddr("198.51.100.1"),
		netip.MustParseAddr("203.0.113.10"),
	}
	dump := &fakeSockDiagTransport{packets: []netlinkPacket{
		{data: joinNetlinkMessages(
			testInetDiagMessage(1, unix.AF_INET6, netip.MustParseAddr("::ffff:203.0.113.10"), netip.MustParseAddr("2001:db8::1"), 101),
			testInetDiagMessage(1, unix.AF_INET6, netip.MustParseAddr("2001:db8::2"), netip.MustParseAddr("2001:db8::3"), 102),
			testDoneMessage(1),
		)},
		{data: joinNetlinkMessages(
			testInetDiagMessage(2, unix.AF_INET, netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("198.51.100.1"), 201),
			testDoneMessage(2),
		)},
	}}
	destroy := &fakeSockDiagTransport{packets: []netlinkPacket{
		{data: testAckMessage(1, unix.SOCK_DESTROY, 0)},
		{data: testAckMessage(2, unix.SOCK_DESTROY, unix.ENOENT)},
	}}

	if err := killSocketsByAddrs(context.Background(), targets, fakeSockDiagFactory(dump, destroy)); err != nil {
		t.Fatalf("kill mapped and native sockets: %v", err)
	}
	if len(dump.sent) != 2 {
		t.Fatalf("dump request count = %d, want 2", len(dump.sent))
	}
	if got := dump.sent[0][unix.NLMSG_HDRLEN]; got != unix.AF_INET6 {
		t.Fatalf("first dump family = %d, want AF_INET6", got)
	}
	if got := dump.sent[1][unix.NLMSG_HDRLEN]; got != unix.AF_INET {
		t.Fatalf("second dump family = %d, want AF_INET", got)
	}
	if len(destroy.sent) != 2 {
		t.Fatalf("destroy request count = %d, want 2", len(destroy.sent))
	}
	if got := destroy.sent[0][unix.NLMSG_HDRLEN]; got != unix.AF_INET6 {
		t.Fatalf("mapped destroy family = %d, want AF_INET6", got)
	}
	if got := destroy.sent[1][unix.NLMSG_HDRLEN]; got != unix.AF_INET {
		t.Fatalf("native destroy family = %d, want AF_INET", got)
	}
	if !dump.closed || !destroy.closed {
		t.Fatal("netlink transports were not closed")
	}
}

func TestKillSocketsByIPsValidatesBeforeOpeningNetlink(t *testing.T) {
	t.Parallel()

	if err := KillSocketsByIPs(context.Background(), nil); err != nil {
		t.Fatalf("empty target set failed: %v", err)
	}
	if err := KillSocketsByIPs(context.Background(), []netip.Addr{{}}); err == nil {
		t.Fatal("invalid batch target was accepted")
	}
	if err := KillSocketsByIPs(context.Background(), []netip.Addr{
		netip.MustParseAddr("fe80::1%eth0"),
	}); err == nil {
		t.Fatal("scoped batch target was accepted")
	}
}

func TestKillSocketsByAddrNoMatchesIsSuccessful(t *testing.T) {
	t.Parallel()

	dump := &fakeSockDiagTransport{packets: []netlinkPacket{
		{data: joinNetlinkMessages(
			testInetDiagMessage(1, unix.AF_INET6, netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::2"), 1),
			testDoneMessage(1),
		)},
		{data: testDoneMessage(2)},
	}}
	destroy := &fakeSockDiagTransport{}
	if err := killSocketsByAddr(
		context.Background(),
		netip.MustParseAddr("203.0.113.10"),
		fakeSockDiagFactory(dump, destroy),
	); err != nil {
		t.Fatalf("empty match set failed: %v", err)
	}
	if len(destroy.sent) != 0 {
		t.Fatalf("destroy requests = %d, want 0", len(destroy.sent))
	}
}

func TestKillSocketsByAddrDestroyAckSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		errno   unix.Errno
		wantErr bool
	}{
		{name: "success", errno: 0},
		{name: "already gone", errno: unix.ENOENT},
		{name: "permission denied", errno: unix.EPERM, wantErr: true},
		{name: "unsupported", errno: unix.EOPNOTSUPP, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dump := &fakeSockDiagTransport{packets: []netlinkPacket{
				{data: joinNetlinkMessages(
					testInetDiagMessage(1, unix.AF_INET6, netip.MustParseAddr("2001:db8::10"), netip.MustParseAddr("2001:db8::20"), 1),
					testDoneMessage(1),
				)},
				{data: testDoneMessage(2)},
			}}
			destroy := &fakeSockDiagTransport{packets: []netlinkPacket{{
				data: testAckMessage(1, unix.SOCK_DESTROY, test.errno),
			}}}
			err := killSocketsByAddr(
				context.Background(),
				netip.MustParseAddr("2001:db8::10"),
				fakeSockDiagFactory(dump, destroy),
			)
			if test.wantErr {
				if !errors.Is(err, test.errno) {
					t.Fatalf("error = %v, want errno %v", err, test.errno)
				}
				return
			}
			if err != nil {
				t.Fatalf("idempotent ACK failed: %v", err)
			}
		})
	}
}

func TestKillSocketsByAddrRejectsUntrustedOrInterruptedDump(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		packet netlinkPacket
	}{
		{
			name:   "non-kernel sender",
			packet: netlinkPacket{data: testDoneMessage(1), senderPID: 42},
		},
		{
			name:   "wrong sequence",
			packet: netlinkPacket{data: testDoneMessage(99)},
		},
		{
			name: "interrupted dump",
			packet: netlinkPacket{data: marshalNetlinkMessage(
				unix.NLMSG_DONE,
				unix.NLM_F_MULTI|unix.NLM_F_DUMP_INTR,
				1,
				make([]byte, 4),
			)},
		},
		{
			name: "truncated diag record",
			packet: netlinkPacket{data: marshalNetlinkMessage(
				unix.SOCK_DIAG_BY_FAMILY,
				unix.NLM_F_MULTI,
				1,
				make([]byte, inetDiagMessageMinLen-1),
			)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dump := &fakeSockDiagTransport{packets: []netlinkPacket{test.packet}}
			destroy := &fakeSockDiagTransport{}
			if err := killSocketsByAddr(
				context.Background(),
				netip.MustParseAddr("203.0.113.10"),
				fakeSockDiagFactory(dump, destroy),
			); err == nil {
				t.Fatal("invalid dump was accepted")
			}
		})
	}
}

func TestDestroyInetDiagSocketValidatesEmbeddedAckRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ack  []byte
	}{
		{name: "wrong embedded type", ack: testAckMessage(1, unix.SOCK_DIAG_BY_FAMILY, 0)},
		{name: "wrong outer sequence", ack: testAckMessage(2, unix.SOCK_DESTROY, 0)},
		{name: "truncated error", ack: marshalNetlinkMessage(unix.NLMSG_ERROR, 0, 1, make([]byte, 8))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transport := &fakeSockDiagTransport{packets: []netlinkPacket{{data: test.ack}}}
			var sequence uint32
			if err := destroyInetDiagSocket(
				context.Background(),
				transport,
				inetDiagSocket{family: unix.AF_INET},
				&sequence,
			); err == nil {
				t.Fatal("invalid destroy ACK was accepted")
			}
		})
	}
}

func TestWalkNetlinkMessagesRejectsMalformedDatagrams(t *testing.T) {
	t.Parallel()

	validHeader := marshalNetlinkMessage(unix.NLMSG_NOOP, 0, 1, nil)
	declaredTooLong := append([]byte(nil), validHeader...)
	binary.NativeEndian.PutUint32(declaredTooLong[:4], uint32(len(declaredTooLong)+4))
	shortLength := append([]byte(nil), validHeader...)
	binary.NativeEndian.PutUint32(shortLength[:4], unix.NLMSG_HDRLEN-1)
	missingPadding := marshalNetlinkMessage(unix.NLMSG_NOOP, 0, 1, []byte{1})
	missingPadding = append(missingPadding, 0)

	for name, datagram := range map[string][]byte{
		"empty":             nil,
		"short header":      make([]byte, unix.NLMSG_HDRLEN-1),
		"short length":      shortLength,
		"declared too long": declaredTooLong,
		"truncated padding": missingPadding,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := walkNetlinkMessages(datagram, func(netlinkHeader, []byte) error { return nil }); err == nil {
				t.Fatal("malformed datagram was accepted")
			}
		})
	}
}

func TestInetDiagRequestEncoding(t *testing.T) {
	t.Parallel()

	dump := buildInetDiagDumpRequest(unix.AF_INET6, 17)
	header, err := decodeNetlinkHeader(dump)
	if err != nil {
		t.Fatal(err)
	}
	if header.message != unix.SOCK_DIAG_BY_FAMILY || header.sequence != 17 ||
		header.flags != unix.NLM_F_REQUEST|unix.NLM_F_DUMP {
		t.Fatalf("dump header = %#v", header)
	}
	payload := dump[unix.NLMSG_HDRLEN:]
	if payload[0] != unix.AF_INET6 || payload[1] != unix.IPPROTO_TCP {
		t.Fatalf("dump request family/protocol = %d/%d", payload[0], payload[1])
	}
	if got := binary.NativeEndian.Uint32(payload[4:8]); got != inetDiagConnectedStates {
		t.Fatalf("dump states = %#x, want %#x", got, uint32(inetDiagConnectedStates))
	}

	var socket inetDiagSocket
	socket.family = unix.AF_INET
	for index := range socket.id {
		socket.id[index] = byte(index + 1)
	}
	destroy := buildInetDiagDestroyRequest(socket, 23)
	destroyHeader, err := decodeNetlinkHeader(destroy)
	if err != nil {
		t.Fatal(err)
	}
	if destroyHeader.message != unix.SOCK_DESTROY || destroyHeader.sequence != 23 ||
		destroyHeader.flags != unix.NLM_F_REQUEST|unix.NLM_F_ACK {
		t.Fatalf("destroy header = %#v", destroyHeader)
	}
	destroyPayload := destroy[unix.NLMSG_HDRLEN:]
	if destroyPayload[0] != unix.AF_INET || destroyPayload[1] != unix.IPPROTO_TCP {
		t.Fatalf("destroy request family/protocol = %d/%d", destroyPayload[0], destroyPayload[1])
	}
	if string(destroyPayload[8:]) != string(socket.id[:]) {
		t.Fatal("destroy request did not preserve inet_diag_sockid")
	}
}

func TestKillSocketsByAddrHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	dump := &fakeSockDiagTransport{receiveFn: func(ctx context.Context) (netlinkPacket, error) {
		<-ctx.Done()
		return netlinkPacket{}, ctx.Err()
	}}
	destroy := &fakeSockDiagTransport{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := killSocketsByAddr(
		ctx,
		netip.MustParseAddr("203.0.113.10"),
		fakeSockDiagFactory(dump, destroy),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestKillSocketsByIPRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"not-an-ip", "fe80::1%eth0"} {
		if err := KillSocketsByIP(context.Background(), input); err == nil {
			t.Fatalf("invalid input %q was accepted", input)
		}
	}
}

func testInetDiagMessage(sequence uint32, family uint8, source, destination netip.Addr, cookie uint32) []byte {
	payload := make([]byte, inetDiagMessageMinLen)
	payload[0] = family
	payload[1] = 1 // TCP_ESTABLISHED
	encodeTestDiagAddress(payload[8:24], family, source)
	encodeTestDiagAddress(payload[24:40], family, destination)
	binary.NativeEndian.PutUint32(payload[44:48], cookie)
	binary.NativeEndian.PutUint32(payload[48:52], cookie+1)
	return marshalNetlinkMessage(unix.SOCK_DIAG_BY_FAMILY, unix.NLM_F_MULTI, sequence, payload)
}

func encodeTestDiagAddress(destination []byte, family uint8, address netip.Addr) {
	switch family {
	case unix.AF_INET:
		value := address.Unmap().As4()
		copy(destination, value[:])
	case unix.AF_INET6:
		value := address.As16()
		copy(destination, value[:])
	default:
		panic(fmt.Sprintf("unsupported test address family %d", family))
	}
}

func testDoneMessage(sequence uint32) []byte {
	return marshalNetlinkMessage(unix.NLMSG_DONE, unix.NLM_F_MULTI, sequence, make([]byte, 4))
}

func testAckMessage(sequence uint32, embeddedType uint16, errno unix.Errno) []byte {
	payload := make([]byte, 4+unix.NLMSG_HDRLEN)
	if errno != 0 {
		status := -int32(errno)
		binary.NativeEndian.PutUint32(payload[:4], uint32(status))
	}
	embedded := marshalNetlinkMessage(embeddedType, unix.NLM_F_REQUEST|unix.NLM_F_ACK, sequence, make([]byte, inetDiagRequestPayloadLen))
	copy(payload[4:], embedded[:unix.NLMSG_HDRLEN])
	return marshalNetlinkMessage(unix.NLMSG_ERROR, 0, sequence, payload)
}

func joinNetlinkMessages(messages ...[]byte) []byte {
	var result []byte
	for _, message := range messages {
		result = append(result, message...)
		padding := netlinkAlign(len(message)) - len(message)
		result = append(result, make([]byte, padding)...)
	}
	return result
}
