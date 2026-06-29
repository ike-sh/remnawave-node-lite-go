package xtls

import (
	"context"
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client wraps a gRPC connection to the local Xray API.
type Client struct {
	mu   sync.Mutex
	conn *grpc.ClientConn
}

// NewClient dials the rw-core gRPC API over a Linux abstract unix socket.
//
// Aligns with remnawave/node 2.8.0, which moved the Xray API off a TLS TCP port
// onto an abstract unix socket (XRAY_API_INBOUND_MODEL). The socket lives in the
// abstract namespace (leading "@"), reachable only from the same network
// namespace, so no internal mTLS is required.
func NewClient(socketName string) (*Client, error) {
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", "@"+socketName)
	}
	conn, err := grpc.NewClient(
		"passthrough:///"+socketName,
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(100*1024*1024)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial xray grpc: %w", err)
	}

	return &Client{conn: conn}, nil
}

func (c *Client) Conn() *grpc.ClientConn {
	return c.conn
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}
