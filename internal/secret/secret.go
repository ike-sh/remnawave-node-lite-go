package secret

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Payload struct {
	CACertPEM    string `json:"caCertPem"`
	JWTPublicKey string `json:"jwtPublicKey"`
	NodeCertPEM  string `json:"nodeCertPem"`
	NodeKeyPEM   string `json:"nodeKeyPem"`
}

var (
	beginPEMRe = regexp.MustCompile(`(-----BEGIN [A-Z ]+-----)`)
	endPEMRe   = regexp.MustCompile(`(-----END [A-Z ]+-----)`)
	newlinesRe = regexp.MustCompile(`\n+`)
)

func Parse(encoded string) (Payload, error) {
	if strings.TrimSpace(encoded) == "" {
		return Payload{}, errors.New("SECRET_KEY is empty")
	}

	raw, err := decodeBase64(encoded)
	if err != nil {
		return Payload{}, fmt.Errorf("decode SECRET_KEY: %w", err)
	}

	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Payload{}, fmt.Errorf("parse SECRET_KEY JSON: %w", err)
	}

	payload.CACertPEM = NormalizePEM(payload.CACertPEM)
	payload.JWTPublicKey = NormalizePEM(payload.JWTPublicKey)
	payload.NodeCertPEM = NormalizePEM(payload.NodeCertPEM)
	payload.NodeKeyPEM = NormalizePEM(payload.NodeKeyPEM)

	if err := payload.Validate(); err != nil {
		return Payload{}, err
	}

	return payload, nil
}

func (p Payload) Validate() error {
	missing := make([]string, 0, 4)
	if p.CACertPEM == "" {
		missing = append(missing, "caCertPem")
	}
	if p.JWTPublicKey == "" {
		missing = append(missing, "jwtPublicKey")
	}
	if p.NodeCertPEM == "" {
		missing = append(missing, "nodeCertPem")
	}
	if p.NodeKeyPEM == "" {
		missing = append(missing, "nodeKeyPem")
	}
	if len(missing) > 0 {
		return fmt.Errorf("SECRET_KEY missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

func NormalizePEM(pemText string) string {
	normalized := strings.ReplaceAll(pemText, `\n`, "\n")
	normalized = strings.ReplaceAll(normalized, "\r\n", "\n")
	normalized = beginPEMRe.ReplaceAllString(normalized, "$1\n")
	normalized = endPEMRe.ReplaceAllString(normalized, "\n$1")
	normalized = newlinesRe.ReplaceAllString(normalized, "\n")
	return strings.TrimSpace(normalized)
}

func decodeBase64(encoded string) ([]byte, error) {
	trimmed := strings.TrimSpace(encoded)
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(trimmed)
}
