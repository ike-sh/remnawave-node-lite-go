package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type JWTValidator struct {
	publicKey *rsa.PublicKey
	now       func() time.Time
}

func NewJWTValidator(publicKeyPEM string) (*JWTValidator, error) {
	publicKey, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return nil, err
	}
	return &JWTValidator{
		publicKey: publicKey,
		now:       time.Now,
	}, nil
}

func (v *JWTValidator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := v.ValidateBearer(r.Header.Get("Authorization")); err != nil {
			writeUnauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (v *JWTValidator) ValidateBearer(header string) error {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return errors.New("missing bearer token")
	}
	return v.Validate(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
}

func (v *JWTValidator) Validate(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("invalid JWT format")
	}

	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := decodeJWTJSON(parts[0], &header); err != nil {
		return fmt.Errorf("decode JWT header: %w", err)
	}
	if header.Algorithm != "RS256" {
		return fmt.Errorf("unsupported JWT alg %q", header.Algorithm)
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("decode JWT signature: %w", err)
	}

	signed := parts[0] + "." + parts[1]
	sum := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(v.publicKey, crypto.SHA256, sum[:], signature); err != nil {
		return fmt.Errorf("verify JWT signature: %w", err)
	}

	var claims map[string]any
	if err := decodeJWTJSON(parts[1], &claims); err != nil {
		return fmt.Errorf("decode JWT claims: %w", err)
	}
	return v.validateTimeClaims(claims)
}

func (v *JWTValidator) validateTimeClaims(claims map[string]any) error {
	now := v.now().Unix()
	if exp, ok, err := numericClaim(claims, "exp"); err != nil {
		return err
	} else if ok && now >= exp {
		return errors.New("JWT is expired")
	}

	if nbf, ok, err := numericClaim(claims, "nbf"); err != nil {
		return err
	} else if ok && now < nbf {
		return errors.New("JWT is not valid yet")
	}

	return nil
}

func numericClaim(claims map[string]any, key string) (int64, bool, error) {
	value, ok := claims[key]
	if !ok {
		return 0, false, nil
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed), true, nil
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, true, err
	default:
		return 0, true, fmt.Errorf("JWT claim %s must be numeric", key)
	}
}

func decodeJWTJSON(segment string, target any) error {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func parseRSAPublicKey(publicKeyPEM string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, errors.New("JWT public key PEM could not be decoded")
	}

	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		if key, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			return key, nil
		}
		return nil, errors.New("certificate does not contain an RSA public key")
	}

	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("public key is not RSA")
	}

	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}

	return nil, errors.New("unsupported JWT public key PEM")
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"message":"Unauthorized","errorCode":"A003"}`))
}
