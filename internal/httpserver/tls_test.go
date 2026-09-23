package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"remnawave-node-lite-go/internal/auth"
	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/secret"
)

func TestBuildTLSConfigGatesCertificateByDerivedSNI(t *testing.T) {
	payload := tlsTestPayload(t)
	config, err := buildTLSConfig(payload, true)
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS13 {
		t.Fatalf("minimum TLS version = %x, want TLS 1.3", config.MinVersion)
	}
	if len(config.Certificates) != 1 {
		t.Fatalf("configured certificates = %d, want 1", len(config.Certificates))
	}
	if config.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v", config.ClientAuth)
	}

	expectedSNI, err := secret.DeriveSNI(payload.CACertPEM, payload.JWTPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	handshake, err := config.GetConfigForClient(&tls.ClientHelloInfo{ServerName: expectedSNI})
	if err != nil {
		t.Fatalf("expected SNI rejected: %v", err)
	}
	if handshake != nil {
		t.Fatal("valid SNI should retain the active server TLS config")
	}

	for _, name := range []string{"", strings.ToUpper(expectedSNI), "wrong.example.com"} {
		if _, err := config.GetConfigForClient(&tls.ClientHelloInfo{ServerName: name}); err == nil {
			t.Fatalf("SNI %q unexpectedly accepted", name)
		}
	}
}

func TestSNIGateRejectsWrongAndMissingSNIAtHandshake(t *testing.T) {
	payload := tlsTestPayload(t)
	serverConfig, err := buildTLSConfig(payload, true)
	if err != nil {
		t.Fatal(err)
	}
	clientCert, err := tls.X509KeyPair([]byte(payload.NodeCertPEM), []byte(payload.NodeKeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(payload.CACertPEM)) {
		t.Fatal("append CA")
	}
	for _, name := range []string{"wrong.example.com", "127.0.0.1"} {
		t.Run(name, func(t *testing.T) {
			serverConn, clientConn := net.Pipe()
			defer serverConn.Close()
			defer clientConn.Close()
			deadline := time.Now().Add(3 * time.Second)
			_ = serverConn.SetDeadline(deadline)
			_ = clientConn.SetDeadline(deadline)
			serverTLS := tls.Server(serverConn, serverConfig)
			clientTLS := tls.Client(clientConn, &tls.Config{MinVersion: tls.VersionTLS13, ServerName: name, RootCAs: roots, Certificates: []tls.Certificate{clientCert}})
			done := make(chan error, 1)
			go func() { done <- serverTLS.HandshakeContext(context.Background()) }()
			_ = clientTLS.HandshakeContext(context.Background())
			if err := <-done; err == nil || !strings.Contains(err.Error(), "unknown sni") {
				t.Fatalf("server handshake error = %v", err)
			}
		})
	}
}

func TestBuildTLSConfigWithoutSNIGateRetainsMutualTLS(t *testing.T) {
	payload := tlsTestPayload(t)
	config, err := buildTLSConfig(payload, false)
	if err != nil {
		t.Fatal(err)
	}
	if config.GetConfigForClient != nil {
		t.Fatal("SNI gate remains enabled")
	}
	if config.MinVersion != tls.VersionTLS13 || config.ClientAuth != tls.RequireAndVerifyClientCert || len(config.Certificates) != 1 || config.ClientCAs == nil {
		t.Fatalf("TLS protections changed: %#v", config)
	}
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			serverConfig, err := buildTLSConfig(payload, enabled)
			if err != nil {
				t.Fatal(err)
			}
			bad := tlsTestPayload(t)
			badCert, err := tls.X509KeyPair([]byte(bad.NodeCertPEM), []byte(bad.NodeKeyPEM))
			if err != nil {
				t.Fatal(err)
			}
			sni, err := secret.DeriveSNI(payload.CACertPEM, payload.JWTPublicKey)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM([]byte(payload.CACertPEM)) {
				t.Fatal("append CA")
			}
			serverConn, clientConn := net.Pipe()
			defer serverConn.Close()
			defer clientConn.Close()
			deadline := time.Now().Add(3 * time.Second)
			_ = serverConn.SetDeadline(deadline)
			_ = clientConn.SetDeadline(deadline)
			serverTLS := tls.Server(serverConn, serverConfig)
			clientTLS := tls.Client(clientConn, &tls.Config{MinVersion: tls.VersionTLS13, ServerName: sni, RootCAs: roots, Certificates: []tls.Certificate{badCert}})
			done := make(chan error, 1)
			go func() { done <- serverTLS.HandshakeContext(context.Background()) }()
			_ = clientTLS.HandshakeContext(context.Background())
			if err := <-done; err == nil {
				t.Fatal("untrusted client certificate accepted")
			}
		})
	}
}

func TestJWTGuardRemainsEnabledWithEitherSNIMode(t *testing.T) {
	payload := tlsTestPayload(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := auth.NewJWTValidator(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})))
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		server, err := New(config.Config{SNIVerification: enabled}, payload, validator, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil)
		expectRequestAbort(t, func() { server.httpServer.Handler.ServeHTTP(httptest.NewRecorder(), req) })
		malformed := httptest.NewRequest(http.MethodPost, "/node/stats/get-users-stats", strings.NewReader("{"))
		malformed.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(rec, malformed)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed JSON should fail before JWT guard: %d", rec.Code)
		}
	}
}

func TestBuildTLSConfigCompletesMutualTLS13Handshake(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "sni-off", true: "sni-on"}[enabled], func(t *testing.T) {
			payload := tlsTestPayload(t)
			serverConfig, err := buildTLSConfig(payload, enabled)
			if err != nil {
				t.Fatal(err)
			}
			serverConfig.NextProtos = []string{"h2", "http/1.1"}
			expectedSNI, err := secret.DeriveSNI(payload.CACertPEM, payload.JWTPublicKey)
			if err != nil {
				t.Fatal(err)
			}
			clientCertificate, err := tls.X509KeyPair([]byte(payload.NodeCertPEM), []byte(payload.NodeKeyPEM))
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM([]byte(payload.CACertPEM)) {
				t.Fatal("append test CA")
			}

			serverConn, clientConn := net.Pipe()
			deadline := time.Now().Add(3 * time.Second)
			_ = serverConn.SetDeadline(deadline)
			_ = clientConn.SetDeadline(deadline)
			serverTLS := tls.Server(serverConn, serverConfig)
			clientTLS := tls.Client(clientConn, &tls.Config{
				MinVersion:   tls.VersionTLS13,
				ServerName:   expectedSNI,
				RootCAs:      roots,
				Certificates: []tls.Certificate{clientCertificate},
				NextProtos:   []string{"h2"},
			})
			defer serverConn.Close()
			defer clientConn.Close()

			serverDone := make(chan error, 1)
			go func() { serverDone <- serverTLS.HandshakeContext(context.Background()) }()
			if err := clientTLS.HandshakeContext(context.Background()); err != nil {
				t.Fatalf("client handshake: %v", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatalf("server handshake: %v", err)
			}
			if got := clientTLS.ConnectionState().Version; got != tls.VersionTLS13 {
				t.Fatalf("negotiated TLS version = %x, want TLS 1.3", got)
			}
			if got := clientTLS.ConnectionState().NegotiatedProtocol; got != "h2" {
				t.Fatalf("negotiated protocol = %q, want h2", got)
			}
		})
	}
}

func tlsTestPayload(t *testing.T) secret.Payload {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwtKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	jwtDER, err := x509.MarshalPKIXPublicKey(&jwtKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	jwtPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: jwtDER}))
	expectedSNI, err := secret.DeriveSNI(caPEM, jwtPEM)
	if err != nil {
		t.Fatal(err)
	}

	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nodeTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: expectedSNI},
		DNSNames:     []string{expectedSNI},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	nodeDER, err := x509.CreateCertificate(rand.Reader, nodeTemplate, caTemplate, &nodeKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	return secret.Payload{
		CACertPEM:    caPEM,
		JWTPublicKey: jwtPEM,
		NodeCertPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: nodeDER})),
		NodeKeyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
	}
}
