package secret

import (
	"encoding/base64"
	"testing"
)

func TestParseSecretKey(t *testing.T) {
	jsonPayload := `{"caCertPem":"-----BEGIN CERTIFICATE-----\\nCA\\n-----END CERTIFICATE-----","jwtPublicKey":"-----BEGIN PUBLIC KEY-----JWT-----END PUBLIC KEY-----","nodeCertPem":"-----BEGIN CERTIFICATE-----NODE-----END CERTIFICATE-----","nodeKeyPem":"-----BEGIN PRIVATE KEY-----KEY-----END PRIVATE KEY-----"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(jsonPayload))

	payload, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if payload.CACertPEM != "-----BEGIN CERTIFICATE-----\nCA\n-----END CERTIFICATE-----" {
		t.Fatalf("unexpected normalized CA cert: %q", payload.CACertPEM)
	}
	if payload.JWTPublicKey == "" || payload.NodeCertPEM == "" || payload.NodeKeyPEM == "" {
		t.Fatal("expected all required fields to be populated")
	}
}

func TestParseSecretKeyRejectsMissingFields(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"caCertPem":"x"}`))
	if _, err := Parse(encoded); err == nil {
		t.Fatal("expected missing fields to fail")
	}
}
