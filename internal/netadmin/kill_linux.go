//go:build linux

package netadmin

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"golang.org/x/sys/unix"
)

const (
	socketKillTimeout = 3 * time.Second

	inetDiagRequestPayloadLen = 56
	inetDiagMessageMinLen     = 72
	inetDiagSocketIDLen       = 48
	netlinkReceiveBufferSize  = 64 << 10
	netlinkPollInterval       = 100 * time.Millisecond

	// Mirrors iproute2's SS_CONN: connected TCP states for which socket
	// destruction is meaningful, excluding LISTEN, CLOSE, TIME_WAIT, and
	// SYN_RECV.
	inetDiagConnectedStates = ((1 << 14) - 1) &^ ((1 << 10) | (1 << 7) | (1 << 6) | (1 << 3))
)

type netlinkHeader struct {
	length   uint32
	message  uint16
	flags    uint16
	sequence uint32
	pid      uint32
}

type inetDiagSocket struct {
	family uint8
	id     [inetDiagSocketIDLen]byte
}

type socketKillTargets map[netip.Addr]struct{}

type netlinkPacket struct {
	data         []byte
	senderPID    uint32
	senderGroups uint32
}

type sockDiagTransport interface {
	send(context.Context, []byte) error
	receive(context.Context) (netlinkPacket, error)
	localPortID() uint32
	close() error
}

type sockDiagTransportFactory func() (sockDiagTransport, error)

type linuxSockDiagTransport struct {
	fd     int
	portID uint32
	buffer []byte
}

// KillSocketsByIP closes connected TCP sockets whose local or remote address
// matches ip. IPv4 targets also match IPv4-mapped addresses on AF_INET6
// sockets. An empty match set and sockets that disappear before SOCK_DESTROY
// are successful no-ops.
func KillSocketsByIP(ctx context.Context, ip string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("parse socket-kill IP %q: %w", ip, err)
	}
	if addr.Zone() != "" {
		return fmt.Errorf("parse socket-kill IP %q: scoped addresses are not supported", ip)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, socketKillTimeout)
	defer cancel()
	return KillSocketsByIPs(ctx, []netip.Addr{addr})
}

// KillSocketsByIPs closes connected TCP sockets matching any target address.
// It performs one streaming dump per address family regardless of target count.
func KillSocketsByIPs(ctx context.Context, addresses []netip.Addr) error {
	targets, err := normalizeSocketKillTargets(addresses)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, socketKillTimeout)
		defer cancel()
	}

	if err := killSocketsByTargets(ctx, targets, openSockDiagTransport); err != nil {
		return fmt.Errorf("destroy TCP sockets for %d target IPs: %w", len(targets), err)
	}
	return nil
}

func killSocketsByAddr(ctx context.Context, target netip.Addr, factory sockDiagTransportFactory) error {
	return killSocketsByAddrs(ctx, []netip.Addr{target}, factory)
}

func killSocketsByAddrs(ctx context.Context, addresses []netip.Addr, factory sockDiagTransportFactory) error {
	targets, err := normalizeSocketKillTargets(addresses)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	return killSocketsByTargets(ctx, targets, factory)
}

func killSocketsByTargets(ctx context.Context, targets socketKillTargets, factory sockDiagTransportFactory) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("missing socket-diagnostic transport factory")
	}

	dump, err := factory()
	if err != nil {
		return fmt.Errorf("open socket-diagnostic dump transport: %w", err)
	}
	defer dump.close()
	destroy, err := factory()
	if err != nil {
		return fmt.Errorf("open socket-diagnostic destroy transport: %w", err)
	}
	defer destroy.close()

	var dumpSequence uint32
	var destroySequence uint32
	// AF_INET6 comes first so an IPv4 target destroys the mapped endpoint of a
	// dual-stack connection before its native AF_INET peer can close it.
	for _, family := range []uint8{unix.AF_INET6, unix.AF_INET} {
		sequence := nextNetlinkSequence(&dumpSequence)
		request := buildInetDiagDumpRequest(family, sequence)
		if err := dump.send(ctx, request); err != nil {
			return fmt.Errorf("request %s TCP socket dump: %w", familyName(family), err)
		}
		if err := consumeInetDiagDump(ctx, dump, destroy, targets, family, sequence, &destroySequence); err != nil {
			return fmt.Errorf("process %s TCP socket dump: %w", familyName(family), err)
		}
	}
	return nil
}

func consumeInetDiagDump(
	ctx context.Context,
	dump sockDiagTransport,
	destroy sockDiagTransport,
	targets socketKillTargets,
	family uint8,
	sequence uint32,
	destroySequence *uint32,
) error {
	complete := false
	for !complete {
		packet, err := dump.receive(ctx)
		if err != nil {
			return err
		}
		if packet.senderPID != 0 || packet.senderGroups != 0 {
			return fmt.Errorf("socket-diagnostic response came from pid=%d groups=%d, want kernel unicast", packet.senderPID, packet.senderGroups)
		}
		if err := walkNetlinkMessages(packet.data, func(header netlinkHeader, payload []byte) error {
			if header.pid != dump.localPortID() {
				return fmt.Errorf("socket-diagnostic response port id=%d, want %d", header.pid, dump.localPortID())
			}
			if header.sequence != sequence {
				return fmt.Errorf("socket-diagnostic response sequence=%d, want %d", header.sequence, sequence)
			}
			if header.flags&unix.NLM_F_DUMP_INTR != 0 {
				return fmt.Errorf("socket-diagnostic dump was interrupted")
			}
			switch header.message {
			case unix.SOCK_DIAG_BY_FAMILY:
				if complete {
					return fmt.Errorf("socket-diagnostic record followed dump completion")
				}
				socket, matches, err := parseInetDiagSocket(payload, family, targets)
				if err != nil {
					return err
				}
				if !matches {
					return nil
				}
				return destroyInetDiagSocket(ctx, destroy, socket, destroySequence)
			case unix.NLMSG_DONE:
				if complete {
					return fmt.Errorf("duplicate socket-diagnostic dump completion")
				}
				if err := parseNetlinkDone(payload); err != nil {
					return err
				}
				complete = true
				return nil
			case unix.NLMSG_ERROR:
				errno, err := parseNetlinkError(payload, unix.SOCK_DIAG_BY_FAMILY, sequence)
				if err != nil {
					return err
				}
				if errno == 0 {
					return fmt.Errorf("unexpected success ACK for socket-diagnostic dump")
				}
				return fmt.Errorf("socket-diagnostic dump: %w", errno)
			case unix.NLMSG_NOOP:
				return nil
			default:
				return fmt.Errorf("unexpected socket-diagnostic message type %d", header.message)
			}
		}); err != nil {
			return err
		}
	}
	return nil
}

func destroyInetDiagSocket(ctx context.Context, transport sockDiagTransport, socket inetDiagSocket, sequenceCounter *uint32) error {
	sequence := nextNetlinkSequence(sequenceCounter)
	request := buildInetDiagDestroyRequest(socket, sequence)
	if err := transport.send(ctx, request); err != nil {
		return fmt.Errorf("send SOCK_DESTROY request: %w", err)
	}
	for {
		packet, err := transport.receive(ctx)
		if err != nil {
			return fmt.Errorf("receive SOCK_DESTROY ACK: %w", err)
		}
		if packet.senderPID != 0 || packet.senderGroups != 0 {
			return fmt.Errorf("SOCK_DESTROY ACK came from pid=%d groups=%d, want kernel unicast", packet.senderPID, packet.senderGroups)
		}
		acknowledged := false
		var ackErr error
		if err := walkNetlinkMessages(packet.data, func(header netlinkHeader, payload []byte) error {
			if header.pid != transport.localPortID() {
				return fmt.Errorf("SOCK_DESTROY ACK port id=%d, want %d", header.pid, transport.localPortID())
			}
			if header.sequence != sequence {
				return fmt.Errorf("SOCK_DESTROY ACK sequence=%d, want %d", header.sequence, sequence)
			}
			switch header.message {
			case unix.NLMSG_ERROR:
				if acknowledged {
					return fmt.Errorf("duplicate SOCK_DESTROY ACK")
				}
				errno, err := parseNetlinkError(payload, unix.SOCK_DESTROY, sequence)
				if err != nil {
					return err
				}
				acknowledged = true
				if errno != 0 && !errors.Is(errno, unix.ENOENT) {
					ackErr = fmt.Errorf("SOCK_DESTROY: %w", errno)
				}
				return nil
			case unix.NLMSG_NOOP:
				return nil
			default:
				return fmt.Errorf("unexpected SOCK_DESTROY response type %d", header.message)
			}
		}); err != nil {
			return err
		}
		if acknowledged {
			return ackErr
		}
	}
}

func parseInetDiagSocket(payload []byte, expectedFamily uint8, targets socketKillTargets) (inetDiagSocket, bool, error) {
	if len(payload) < inetDiagMessageMinLen {
		return inetDiagSocket{}, false, fmt.Errorf("truncated inet_diag_msg: got %d bytes, want at least %d", len(payload), inetDiagMessageMinLen)
	}
	family := payload[0]
	if family != expectedFamily {
		return inetDiagSocket{}, false, fmt.Errorf("inet_diag_msg family=%d, want %d", family, expectedFamily)
	}
	if family != unix.AF_INET && family != unix.AF_INET6 {
		return inetDiagSocket{}, false, fmt.Errorf("unsupported inet_diag_msg family %d", family)
	}

	source, err := inetDiagAddress(family, payload[8:24])
	if err != nil {
		return inetDiagSocket{}, false, fmt.Errorf("decode inet_diag source: %w", err)
	}
	destination, err := inetDiagAddress(family, payload[24:40])
	if err != nil {
		return inetDiagSocket{}, false, fmt.Errorf("decode inet_diag destination: %w", err)
	}
	_, sourceMatches := targets[source.Unmap()]
	_, destinationMatches := targets[destination.Unmap()]
	if !sourceMatches && !destinationMatches {
		return inetDiagSocket{}, false, nil
	}

	socket := inetDiagSocket{family: family}
	copy(socket.id[:], payload[4:52])
	return socket, true, nil
}

func normalizeSocketKillTargets(addresses []netip.Addr) (socketKillTargets, error) {
	targets := make(socketKillTargets, len(addresses))
	for index, address := range addresses {
		if !address.IsValid() {
			return nil, fmt.Errorf("socket-kill address %d is invalid", index)
		}
		if address.Zone() != "" {
			return nil, fmt.Errorf("socket-kill address %d is scoped", index)
		}
		targets[address.Unmap()] = struct{}{}
	}
	return targets, nil
}

func inetDiagAddress(family uint8, encoded []byte) (netip.Addr, error) {
	switch family {
	case unix.AF_INET:
		if len(encoded) < 4 {
			return netip.Addr{}, fmt.Errorf("truncated IPv4 address")
		}
		var address [4]byte
		copy(address[:], encoded[:4])
		return netip.AddrFrom4(address), nil
	case unix.AF_INET6:
		if len(encoded) < 16 {
			return netip.Addr{}, fmt.Errorf("truncated IPv6 address")
		}
		var address [16]byte
		copy(address[:], encoded[:16])
		return netip.AddrFrom16(address), nil
	default:
		return netip.Addr{}, fmt.Errorf("unsupported address family %d", family)
	}
}

func buildInetDiagDumpRequest(family uint8, sequence uint32) []byte {
	payload := make([]byte, inetDiagRequestPayloadLen)
	payload[0] = family
	payload[1] = unix.IPPROTO_TCP
	binary.NativeEndian.PutUint32(payload[4:8], inetDiagConnectedStates)
	return marshalNetlinkMessage(unix.SOCK_DIAG_BY_FAMILY, unix.NLM_F_REQUEST|unix.NLM_F_DUMP, sequence, payload)
}

func buildInetDiagDestroyRequest(socket inetDiagSocket, sequence uint32) []byte {
	payload := make([]byte, inetDiagRequestPayloadLen)
	payload[0] = socket.family
	payload[1] = unix.IPPROTO_TCP
	copy(payload[8:], socket.id[:])
	return marshalNetlinkMessage(unix.SOCK_DESTROY, unix.NLM_F_REQUEST|unix.NLM_F_ACK, sequence, payload)
}

func marshalNetlinkMessage(messageType, flags uint16, sequence uint32, payload []byte) []byte {
	length := unix.NLMSG_HDRLEN + len(payload)
	message := make([]byte, length)
	binary.NativeEndian.PutUint32(message[0:4], uint32(length))
	binary.NativeEndian.PutUint16(message[4:6], messageType)
	binary.NativeEndian.PutUint16(message[6:8], flags)
	binary.NativeEndian.PutUint32(message[8:12], sequence)
	copy(message[unix.NLMSG_HDRLEN:], payload)
	return message
}

func walkNetlinkMessages(data []byte, visit func(netlinkHeader, []byte) error) error {
	if len(data) == 0 {
		return fmt.Errorf("empty netlink datagram")
	}
	for offset := 0; offset < len(data); {
		remaining := len(data) - offset
		if remaining < unix.NLMSG_HDRLEN {
			return fmt.Errorf("truncated netlink header: %d trailing bytes", remaining)
		}
		header, err := decodeNetlinkHeader(data[offset:])
		if err != nil {
			return err
		}
		messageLength := int(header.length)
		if messageLength > remaining {
			return fmt.Errorf("truncated netlink message: header length=%d, available=%d", messageLength, remaining)
		}
		if err := visit(header, data[offset+unix.NLMSG_HDRLEN:offset+messageLength]); err != nil {
			return err
		}
		alignedLength := netlinkAlign(messageLength)
		if alignedLength > remaining {
			if messageLength != remaining {
				return fmt.Errorf("truncated netlink message padding")
			}
			alignedLength = messageLength
		}
		offset += alignedLength
	}
	return nil
}

func decodeNetlinkHeader(data []byte) (netlinkHeader, error) {
	if len(data) < unix.NLMSG_HDRLEN {
		return netlinkHeader{}, fmt.Errorf("truncated netlink header")
	}
	header := netlinkHeader{
		length:   binary.NativeEndian.Uint32(data[0:4]),
		message:  binary.NativeEndian.Uint16(data[4:6]),
		flags:    binary.NativeEndian.Uint16(data[6:8]),
		sequence: binary.NativeEndian.Uint32(data[8:12]),
		pid:      binary.NativeEndian.Uint32(data[12:16]),
	}
	if header.length < unix.NLMSG_HDRLEN {
		return netlinkHeader{}, fmt.Errorf("invalid netlink message length %d", header.length)
	}
	return header, nil
}

func parseNetlinkError(payload []byte, expectedType uint16, expectedSequence uint32) (unix.Errno, error) {
	const netlinkErrorMinLen = 4 + unix.NLMSG_HDRLEN
	if len(payload) < netlinkErrorMinLen {
		return 0, fmt.Errorf("truncated netlink error payload: got %d bytes, want at least %d", len(payload), netlinkErrorMinLen)
	}
	embedded, err := decodeNetlinkHeader(payload[4:])
	if err != nil {
		return 0, fmt.Errorf("decode embedded netlink request: %w", err)
	}
	if embedded.message != expectedType || embedded.sequence != expectedSequence {
		return 0, fmt.Errorf(
			"netlink ACK identifies type=%d sequence=%d, want type=%d sequence=%d",
			embedded.message,
			embedded.sequence,
			expectedType,
			expectedSequence,
		)
	}
	if embedded.length != unix.NLMSG_HDRLEN+inetDiagRequestPayloadLen {
		return 0, fmt.Errorf("netlink ACK embeds request length=%d, want %d", embedded.length, unix.NLMSG_HDRLEN+inetDiagRequestPayloadLen)
	}
	return decodeNetlinkErrno(payload[:4])
}

func parseNetlinkDone(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if len(payload) < 4 {
		return fmt.Errorf("truncated NLMSG_DONE payload")
	}
	errno, err := decodeNetlinkErrno(payload[:4])
	if err != nil {
		return fmt.Errorf("invalid NLMSG_DONE status: %w", err)
	}
	if errno != 0 {
		return fmt.Errorf("socket-diagnostic dump completion: %w", errno)
	}
	return nil
}

func decodeNetlinkErrno(encoded []byte) (unix.Errno, error) {
	if len(encoded) < 4 {
		return 0, fmt.Errorf("truncated netlink errno")
	}
	status := int32(binary.NativeEndian.Uint32(encoded[:4]))
	if status == 0 {
		return 0, nil
	}
	if status > 0 {
		return 0, fmt.Errorf("positive netlink error status %d", status)
	}
	value := -int64(status)
	if value > 4095 {
		return 0, fmt.Errorf("invalid netlink errno %d", value)
	}
	return unix.Errno(value), nil
}

func nextNetlinkSequence(sequence *uint32) uint32 {
	(*sequence)++
	if *sequence == 0 {
		(*sequence)++
	}
	return *sequence
}

func netlinkAlign(length int) int {
	return (length + unix.NLMSG_ALIGNTO - 1) &^ (unix.NLMSG_ALIGNTO - 1)
}

func familyName(family uint8) string {
	switch family {
	case unix.AF_INET:
		return "IPv4"
	case unix.AF_INET6:
		return "IPv6"
	default:
		return fmt.Sprintf("family-%d", family)
	}
}

func openSockDiagTransport() (sockDiagTransport, error) {
	fd, err := unix.Socket(
		unix.AF_NETLINK,
		unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK,
		unix.NETLINK_SOCK_DIAG,
	)
	if err != nil {
		return nil, err
	}
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		unix.Close(fd)
		return nil, err
	}
	local, err := unix.Getsockname(fd)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("read socket-diagnostic local address: %w", err)
	}
	localNetlink, ok := local.(*unix.SockaddrNetlink)
	if !ok || localNetlink == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("socket-diagnostic local address has unexpected type %T", local)
	}
	if localNetlink.Pid == 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("socket-diagnostic local port id is zero")
	}
	return &linuxSockDiagTransport{
		fd:     fd,
		portID: localNetlink.Pid,
		buffer: make([]byte, netlinkReceiveBufferSize),
	}, nil
}

func (t *linuxSockDiagTransport) localPortID() uint32 {
	if t == nil {
		return 0
	}
	return t.portID
}

func (t *linuxSockDiagTransport) close() error {
	if t == nil || t.fd < 0 {
		return nil
	}
	err := unix.Close(t.fd)
	t.fd = -1
	return err
}

func (t *linuxSockDiagTransport) send(ctx context.Context, message []byte) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Sendto(t.fd, message, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK})
		if err == nil {
			return nil
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EAGAIN) {
			return err
		}
		if err := waitNetlinkFD(ctx, t.fd, unix.POLLOUT); err != nil {
			return err
		}
	}
}

func (t *linuxSockDiagTransport) receive(ctx context.Context) (netlinkPacket, error) {
	for {
		if err := waitNetlinkFD(ctx, t.fd, unix.POLLIN); err != nil {
			return netlinkPacket{}, err
		}
		n, _, flags, sender, err := unix.Recvmsg(t.fd, t.buffer, nil, 0)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return netlinkPacket{}, err
		}
		if flags&unix.MSG_TRUNC != 0 {
			return netlinkPacket{}, fmt.Errorf("truncated socket-diagnostic netlink datagram")
		}
		if n == 0 {
			return netlinkPacket{}, fmt.Errorf("empty socket-diagnostic netlink datagram")
		}
		netlinkSender, ok := sender.(*unix.SockaddrNetlink)
		if !ok || netlinkSender == nil {
			return netlinkPacket{}, fmt.Errorf("socket-diagnostic response has unexpected sender %T", sender)
		}
		return netlinkPacket{
			data:         t.buffer[:n],
			senderPID:    netlinkSender.Pid,
			senderGroups: netlinkSender.Groups,
		}, nil
	}
}

func waitNetlinkFD(ctx context.Context, fd int, events int16) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		timeout, err := netlinkPollTimeout(ctx)
		if err != nil {
			return err
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: events}}
		ready, err := unix.Poll(fds, timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if ready == 0 {
			continue
		}
		revents := fds[0].Revents
		if revents&events != 0 {
			return nil
		}
		if revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return fmt.Errorf("socket-diagnostic netlink poll revents=0x%x", revents)
		}
	}
}

func netlinkPollTimeout(ctx context.Context) (int, error) {
	wait := netlinkPollInterval
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return 0, context.DeadlineExceeded
		}
		if remaining < wait {
			wait = remaining
		}
	}
	milliseconds := (wait + time.Millisecond - 1) / time.Millisecond
	if milliseconds < 1 {
		milliseconds = 1
	}
	return int(milliseconds), nil
}
