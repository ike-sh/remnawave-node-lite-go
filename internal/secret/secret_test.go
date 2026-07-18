package secret

import (
	"encoding/base64"
	"strings"
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

func TestParseSecretKeyBase64Encodings(t *testing.T) {
	const jsonPayload = `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key","extra":"??"}`
	tests := []struct {
		name       string
		encoding   *base64.Encoding
		urlSafe    bool
		wantPadded bool
	}{
		{name: "standard", encoding: base64.StdEncoding, wantPadded: true},
		{name: "raw standard", encoding: base64.RawStdEncoding},
		{name: "URL", encoding: base64.URLEncoding, urlSafe: true, wantPadded: true},
		{name: "raw URL", encoding: base64.RawURLEncoding, urlSafe: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded := test.encoding.EncodeToString([]byte(jsonPayload))
			if test.urlSafe && !strings.ContainsAny(encoded, "-_") {
				t.Fatalf("URL-safe fixture %q does not contain '-' or '_'", encoded)
			}
			if test.wantPadded != strings.Contains(encoded, "=") {
				t.Fatalf("padding presence = %t, want %t", strings.Contains(encoded, "="), test.wantPadded)
			}

			payload, err := Parse(encoded)
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if payload.CACertPEM != "ca" || payload.JWTPublicKey != "jwt" || payload.NodeCertPEM != "cert" || payload.NodeKeyPEM != "key" {
				t.Fatalf("unexpected payload: %#v", payload)
			}
		})
	}
}

func TestParseSecretKeyAllowsUnknownFields(t *testing.T) {
	tests := []struct {
		name  string
		extra string
	}{
		{name: "string", extra: `"value"`},
		{name: "number", extra: `42`},
		{name: "null", extra: `null`},
		{name: "nested object", extra: `{"nested":[true,{"value":1}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key","extra":` + test.extra + `}`
			encoded := base64.StdEncoding.EncodeToString([]byte(raw))
			if _, err := Parse(encoded); err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
		})
	}
}

func TestParseSecretKeyRejectsMissingFields(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"caCertPem":"x"}`))
	if _, err := Parse(encoded); err == nil {
		t.Fatal("expected missing fields to fail")
	}
}

func TestParseSecretKeyRejectsInvalidJSONShape(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "trailing data",
			raw:  `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}trailing}`,
		},
		{
			name: "duplicate field",
			raw:  `{"caCertPem":"first","caCertPem":"second","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`,
		},
		{
			name: "non string field",
			raw:  `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":42}`,
		},
		{
			name: "duplicate unknown field",
			raw:  `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key","extra":1,"extra":2}`,
		},
		{
			name: "second JSON document",
			raw:  `{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"} {}`,
		},
		{
			name: "top level array",
			raw:  `["ca","jwt","cert","key"]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString([]byte(test.raw))
			if _, err := Parse(encoded); err == nil {
				t.Fatal("expected invalid SECRET_KEY JSON to fail")
			}
		})
	}
}

func TestParseSecretKeyEncodedSizeBoundary(t *testing.T) {
	const prefix = `{"caCertPem":"`
	const suffix = `","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}`
	rawLength := base64.StdEncoding.DecodedLen(MaxEncodedBytes)
	raw := prefix + strings.Repeat("a", rawLength-len(prefix)-len(suffix)) + suffix
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	if len(encoded) != MaxEncodedBytes {
		t.Fatalf("encoded length = %d, want %d", len(encoded), MaxEncodedBytes)
	}
	if _, err := Parse(encoded); err != nil {
		t.Fatalf("Parse exact limit: %v", err)
	}
	if _, err := Parse(encoded + "A"); err == nil {
		t.Fatal("expected value above encoded size limit to fail")
	}
}
