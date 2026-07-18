package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"
)

func testJWTKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
}

func TestJWTValidator(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)

	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{"exp": 2000})
	for _, header := range []string{
		"Bearer " + token,
		"bearer " + token,
		"BEARER\t" + token,
		"Bearer " + token + " trailing",
	} {
		if err := validator.ValidateBearer(header); err != nil {
			t.Errorf("ValidateBearer(%q) returned error: %v", header[:6], err)
		}
	}
	for _, header := range []string{"", "Basic " + token, "Bearer"} {
		if err := validator.ValidateBearer(header); err == nil {
			t.Errorf("ValidateBearer(%q) accepted malformed authorization header", header)
		}
	}
}

func TestJWTValidatorDefaultIgnoresIdentityClaims(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"exp": 2000,
		"iss": "evil",
		"aud": []any{"other-service"},
		"sub": 42,
	})
	if err := validator.Validate(token); err != nil {
		t.Fatalf("default validator rejected an official-compatible token: %v", err)
	}
}

func TestJWTValidatorWithClaimsRequiresExactIdentityClaims(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidatorWithClaims(publicPEM, ClaimExpectations{
		Issuer:   "remnawave",
		Audience: "remnawave-node",
		Subject:  "remnawave-backend",
	})
	if err != nil {
		t.Fatalf("NewJWTValidatorWithClaims: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	tests := []struct {
		name    string
		claims  map[string]any
		wantErr bool
	}{
		{
			name: "matching",
			claims: map[string]any{
				"exp": 2000,
				"iss": "remnawave",
				"aud": []any{"remnawave-node"},
				"sub": "remnawave-backend",
			},
		},
		{name: "missing issuer", claims: map[string]any{"exp": 2000, "aud": "remnawave-node", "sub": "remnawave-backend"}, wantErr: true},
		{name: "missing audience", claims: map[string]any{"exp": 2000, "iss": "remnawave", "sub": "remnawave-backend"}, wantErr: true},
		{name: "missing subject", claims: map[string]any{"exp": 2000, "iss": "remnawave", "aud": "remnawave-node"}, wantErr: true},
		{name: "wrong issuer", claims: map[string]any{"exp": 2000, "iss": "other", "aud": "remnawave-node", "sub": "remnawave-backend"}, wantErr: true},
		{name: "wrong audience", claims: map[string]any{"exp": 2000, "iss": "remnawave", "aud": "other", "sub": "remnawave-backend"}, wantErr: true},
		{name: "wrong subject", claims: map[string]any{"exp": 2000, "iss": "remnawave", "aud": "remnawave-node", "sub": "other"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, tt.claims)
			err := validator.Validate(token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestJWTValidatorRejectsExpired(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)

	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(3000, 0) }

	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{"exp": 2000})
	if err := validator.Validate(token); err == nil {
		t.Fatal("expected expired token to fail")
	}
}

func TestJWTValidatorRejectsTrailingJSON(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	tests := []struct {
		name      string
		header    string
		claims    string
		wantError string
	}{
		{
			name:      "header",
			header:    `{"alg":"RS256"} {}`,
			claims:    `{"exp":2000}`,
			wantError: "decode JWT header: multiple JSON values",
		},
		{
			name:      "claims",
			header:    `{"alg":"RS256"}`,
			claims:    `{"exp":2000} []`,
			wantError: "decode JWT claims: multiple JSON values",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := signedRawJWT(t, key, test.header, test.claims)
			if err := validator.Validate(token); err == nil || err.Error() != test.wantError {
				t.Fatalf("Validate error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func signedJWT(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()

	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	return signedRawJWT(t, key, string(headerJSON), string(claimsJSON))
}

func signedRawJWT(t *testing.T, key *rsa.PrivateKey, headerJSON, claimsJSON string) string {
	t.Helper()

	encodedHeader := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	encodedClaims := base64.RawURLEncoding.EncodeToString([]byte(claimsJSON))
	signed := encodedHeader + "." + encodedClaims
	sum := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}

	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}
