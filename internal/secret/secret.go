package secret

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const MaxEncodedBytes = 256 << 10

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
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return Payload{}, errors.New("SECRET_KEY is empty")
	}
	if len(trimmed) > MaxEncodedBytes {
		return Payload{}, fmt.Errorf("SECRET_KEY exceeds %d bytes", MaxEncodedBytes)
	}

	raw, err := decodeBase64(trimmed)
	if err != nil {
		return Payload{}, fmt.Errorf("decode SECRET_KEY: %w", err)
	}

	payload, err := decodePayloadJSON(raw)
	if err != nil {
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

func decodePayloadJSON(raw []byte) (Payload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return Payload{}, err
	}
	opening, ok := token.(json.Delim)
	if !ok || opening != '{' {
		return Payload{}, errors.New("top-level value must be an object")
	}

	var payload Payload
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return Payload{}, err
		}
		key, ok := token.(string)
		if !ok {
			return Payload{}, errors.New("object key must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return Payload{}, fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = struct{}{}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return Payload{}, err
		}
		switch key {
		case "caCertPem":
			err = json.Unmarshal(value, &payload.CACertPEM)
		case "jwtPublicKey":
			err = json.Unmarshal(value, &payload.JWTPublicKey)
		case "nodeCertPem":
			err = json.Unmarshal(value, &payload.NodeCertPEM)
		case "nodeKeyPem":
			err = json.Unmarshal(value, &payload.NodeKeyPEM)
		default:
			continue
		}
		if err != nil {
			return Payload{}, fmt.Errorf("field %q must be a string: %w", key, err)
		}
	}

	token, err = decoder.Token()
	if err != nil {
		return Payload{}, err
	}
	closing, ok := token.(json.Delim)
	if !ok || closing != '}' {
		return Payload{}, errors.New("top-level object is not closed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Payload{}, errors.New("trailing JSON value")
		}
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
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}

	var decodeErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(trimmed)
		if err == nil {
			return decoded, nil
		}
		decodeErr = err
	}
	return nil, decodeErr
}
