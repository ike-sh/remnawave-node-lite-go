package secret

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
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

	caCert, err := parseCertificate("caCertPem", p.CACertPEM)
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(caCert.NotBefore) {
		return fmt.Errorf("SECRET_KEY caCertPem is not valid before %s", caCert.NotBefore.UTC().Format(time.RFC3339))
	}
	if now.After(caCert.NotAfter) {
		return fmt.Errorf("SECRET_KEY caCertPem expired at %s", caCert.NotAfter.UTC().Format(time.RFC3339))
	}
	if err := caCert.CheckSignature(caCert.SignatureAlgorithm, caCert.RawTBSCertificate, caCert.Signature); err != nil {
		return fmt.Errorf("SECRET_KEY caCertPem self-signature is invalid: %w", err)
	}

	nodeCert, err := parseCertificate("nodeCertPem", p.NodeCertPEM)
	if err != nil {
		return err
	}
	if now.Before(nodeCert.NotBefore) {
		return fmt.Errorf("SECRET_KEY nodeCertPem is not valid before %s", nodeCert.NotBefore.UTC().Format(time.RFC3339))
	}
	if now.After(nodeCert.NotAfter) {
		return fmt.Errorf("SECRET_KEY nodeCertPem expired at %s", nodeCert.NotAfter.UTC().Format(time.RFC3339))
	}
	if err := caCert.CheckSignature(nodeCert.SignatureAlgorithm, nodeCert.RawTBSCertificate, nodeCert.Signature); err != nil {
		return fmt.Errorf("SECRET_KEY nodeCertPem is not signed by caCertPem: %w", err)
	}

	privatePublic, err := privateKeyPublic(p.NodeKeyPEM)
	if err != nil {
		return fmt.Errorf("SECRET_KEY nodeKeyPem: %w", err)
	}
	certPublicDER, err := x509.MarshalPKIXPublicKey(nodeCert.PublicKey)
	if err != nil {
		return fmt.Errorf("SECRET_KEY nodeCertPem public key: %w", err)
	}
	privatePublicDER, err := x509.MarshalPKIXPublicKey(privatePublic)
	if err != nil {
		return fmt.Errorf("SECRET_KEY nodeKeyPem public key: %w", err)
	}
	if !bytes.Equal(certPublicDER, privatePublicDER) {
		return errors.New("SECRET_KEY nodeKeyPem does not match nodeCertPem")
	}

	if _, err := parsePublicKey(p.JWTPublicKey); err != nil {
		return fmt.Errorf("SECRET_KEY jwtPublicKey: %w", err)
	}
	return nil
}

func parseCertificate(field, value string) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("SECRET_KEY %s is not a PEM certificate", field)
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, fmt.Errorf("SECRET_KEY %s contains trailing PEM data", field)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("SECRET_KEY %s cannot be parsed: %w", field, err)
	}
	return cert, nil
}

func privateKeyPublic(value string) (crypto.PublicKey, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("is not a PEM private key")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("contains trailing PEM data")
	}

	var key any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot be parsed: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("parsed key type %T is not a signer", key)
	}
	return signer.Public(), nil
}

func parsePublicKey(value string) (crypto.PublicKey, error) {
	block, rest := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("is not PEM data")
	}
	if strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("contains trailing PEM data")
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		return cert.PublicKey, nil
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("uses an unsupported public key encoding")
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
