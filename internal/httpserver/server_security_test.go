package httpserver

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/auth"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/config"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/secret"
)

func TestExternalServerSecurityPolicy(t *testing.T) {
	server := newSecurityTestServer(t)
	if got := server.httpServer.TLSConfig.MinVersion; got != tls.VersionTLS13 {
		t.Fatalf("minimum TLS version = %#x, want TLS 1.3", got)
	}
	if server.httpServer.TLSNextProto == nil || len(server.httpServer.TLSNextProto) != 0 {
		t.Fatal("HTTP/2 must be disabled to preserve connection-drop semantics")
	}
	if got := server.httpServer.MaxHeaderBytes; got != 64<<10 {
		t.Fatalf("MaxHeaderBytes = %d, want %d", got, 64<<10)
	}
}

func TestExternalServerDropsUnauthorizedAndUnknownRequests(t *testing.T) {
	server := newSecurityTestServer(t)
	testServer := httptest.NewServer(server.httpServer.Handler)
	defer testServer.Close()

	for _, path := range []string{"/node/xray/healthcheck", "/unknown"} {
		req, err := http.NewRequest(http.MethodGet, testServer.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := testServer.Client().Do(req)
		if response != nil {
			response.Body.Close()
			t.Fatalf("%s returned HTTP %s instead of dropping the connection", path, response.Status)
		}
		if err == nil {
			t.Fatalf("%s returned no client error after dropping the connection", path)
		}
	}
}

func newSecurityTestServer(t *testing.T) *Server {
	t.Helper()
	payload := testTLSPayload(t)
	validator, err := auth.NewJWTValidator(payload.JWTPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(config.Config{}, payload, validator, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func testTLSPayload(t *testing.T) secret.Payload {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "remnanode-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwtDER, err := x509.MarshalPKIXPublicKey(&jwtKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
	return secret.Payload{
		CACertPEM:    certPEM,
		JWTPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: jwtDER})),
		NodeCertPEM:  certPEM,
		NodeKeyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
	}
}
