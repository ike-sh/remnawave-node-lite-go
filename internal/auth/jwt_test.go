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
	"net/http"
	"net/http/httptest"
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

	validator, err := NewJWTValidatorWithClaims(publicPEM, DefaultClaimExpectations())
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{"exp": 2000})
	if err := validator.ValidateBearer("Bearer " + token); err != nil {
		t.Fatalf("ValidateBearer returned error: %v", err)
	}
}

func TestJWTMiddlewareAbortsMissingAndInvalidToken(t *testing.T) {
	_, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatal(err)
	}
	handler := validator.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unauthorized request reached handler") }))
	for _, header := range []string{"", "Bearer invalid.jwt.token"} {
		t.Run(header, func(t *testing.T) {
			defer func() {
				if got := recover(); got != http.ErrAbortHandler {
					t.Fatalf("abort = %v", got)
				}
			}()
			req := httptest.NewRequest(http.MethodGet, "/node/xray/healthcheck", nil)
			req.Header.Set("Authorization", header)
			handler.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

func TestJWTValidatorRejectsMismatchedIssuer(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidatorWithClaims(publicPEM, DefaultClaimExpectations())
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"exp": 2000,
		"iss": "evil",
	})
	if err := validator.Validate(token); err == nil {
		t.Fatal("expected mismatched iss to fail")
	}
}

func TestJWTValidatorDefaultAcceptsDifferentIdentityClaims(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatal(err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }
	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{"exp": 2000, "iss": "other", "aud": "other", "sub": "other"})
	if err := validator.Validate(token); err != nil {
		t.Fatalf("official JWT strategy permits unconstrained identity claims: %v", err)
	}
}

func TestJWTValidatorAcceptsPanelClaims(t *testing.T) {
	key, publicPEM := testJWTKeyPair(t)
	validator, err := NewJWTValidator(publicPEM)
	if err != nil {
		t.Fatalf("NewJWTValidator: %v", err)
	}
	validator.now = func() time.Time { return time.Unix(1000, 0) }

	token := signedJWT(t, key, map[string]any{"alg": "RS256", "typ": "JWT"}, map[string]any{
		"exp": 2000,
		"iss": "remnawave",
		"aud": "remnawave-node",
		"sub": "remnawave-backend",
	})
	if err := validator.Validate(token); err != nil {
		t.Fatalf("Validate returned error: %v", err)
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

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signed := encodedHeader + "." + encodedClaims
	sum := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}

	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}
