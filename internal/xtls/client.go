package xtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const defaultServerName = "internal.remnawave.local"

// TLSCredentials holds PEM material for mTLS gRPC to rw-core API inbound.
type TLSCredentials struct {
	CACertPEM     string
	ClientCertPEM string
	ClientKeyPEM  string
}

// Client wraps a gRPC connection to the local Xray API.
type Client struct {
	mu   sync.Mutex
	conn *grpc.ClientConn
}

func NewClient(address string, creds TLSCredentials) (*Client, error) {
	certificate, err := tls.X509KeyPair([]byte(creds.ClientCertPEM), []byte(creds.ClientKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	caPool := x509.NewCertPool()
	if ok := caPool.AppendCertsFromPEM([]byte(creds.CACertPEM)); !ok {
		return nil, fmt.Errorf("append CA certificate")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		RootCAs:      caPool,
		ServerName:   defaultServerName,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2"},
	}

	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithAuthority(defaultServerName),
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
