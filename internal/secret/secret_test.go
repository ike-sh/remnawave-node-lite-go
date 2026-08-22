package secret

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestParseSecretKey(t *testing.T) {
	payload := validPayload(t)
	parsed, err := Parse(encodePayload(t, payload))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.CACertPEM != strings.TrimSpace(payload.CACertPEM) {
		t.Fatal("unexpected normalized CA certificate")
	}
	if parsed.JWTPublicKey == "" || parsed.NodeCertPEM == "" || parsed.NodeKeyPEM == "" {
		t.Fatal("expected all required fields to be populated")
	}
}

func TestParseSecretKeyNormalizesEscapedPEMNewlines(t *testing.T) {
	payload := validPayload(t)
	payload.CACertPEM = strings.ReplaceAll(payload.CACertPEM, "\n", `\n`)
	payload.JWTPublicKey = strings.ReplaceAll(payload.JWTPublicKey, "\n", `\n`)
	payload.NodeCertPEM = strings.ReplaceAll(payload.NodeCertPEM, "\n", `\n`)
	payload.NodeKeyPEM = strings.ReplaceAll(payload.NodeKeyPEM, "\n", `\n`)

	if _, err := Parse(encodePayload(t, payload)); err != nil {
		t.Fatalf("Parse escaped PEM: %v", err)
	}
}

func TestParseSecretKeyRejectsMissingFields(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"caCertPem":"x"}`))
	if _, err := Parse(encoded); err == nil {
		t.Fatal("expected missing fields to fail")
	}
}

func TestPayloadValidateRejectsMismatchedNodeKey(t *testing.T) {
	payload := validPayload(t)
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	payload.NodeKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))

	if err := payload.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected key mismatch error, got %v", err)
	}
}

func TestPayloadValidateRejectsExpiredCA(t *testing.T) {
	payload := validPayloadAt(t, time.Now().Add(-4*time.Hour), time.Now().Add(-2*time.Hour))
	if err := payload.Validate(); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired certificate error, got %v", err)
	}
}

func TestPayloadValidateRejectsMalformedJWTPublicKey(t *testing.T) {
	payload := validPayload(t)
	payload.JWTPublicKey = "-----BEGIN PUBLIC KEY-----\nbroken\n-----END PUBLIC KEY-----"
	if err := payload.Validate(); err == nil || !strings.Contains(err.Error(), "jwtPublicKey") {
		t.Fatalf("expected JWT key error, got %v", err)
	}
}

func validPayload(t *testing.T) Payload {
	t.Helper()
	return validPayloadAt(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
}

func validPayloadAt(t *testing.T, notBefore, notAfter time.Time) Payload {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
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
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	nodeTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-node"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	nodeDER, err := x509.CreateCertificate(rand.Reader, nodeTemplate, caTemplate, &nodeKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	nodeKeyDER, err := x509.MarshalPKCS8PrivateKey(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	jwtDER, err := x509.MarshalPKIXPublicKey(&jwtKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	return Payload{
		CACertPEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		JWTPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: jwtDER})),
		NodeCertPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: nodeDER})),
		NodeKeyPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: nodeKeyDER})),
	}
}

func encodePayload(t *testing.T, payload Payload) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}
