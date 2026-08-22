package secret

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"

	"golang.org/x/crypto/hkdf"
)

const sniHKDFInfo = "rw-v1"

var (
	sniPEMArmor  = regexp.MustCompile(`-----[^-]+-----`)
	sniNonBase64 = regexp.MustCompile(`[^A-Za-z0-9+/=]`)
	sniTLDs      = [...]string{"com", "net", "org", "io", "dev", "app"}
)

// DeriveSNI reproduces the private server name used by @remnawave/node >= 3.3.0.
// The Panel derives the same value from its JWT public key and node CA certificate.
func DeriveSNI(caCertPEM, jwtPublicKeyPEM string) (string, error) {
	canonical := func(value string) string {
		return sniNonBase64.ReplaceAllString(sniPEMArmor.ReplaceAllString(value, ""), "")
	}
	ikm := []byte(canonical(jwtPublicKeyPEM) + canonical(caCertPEM))
	if len(ikm) == 0 {
		return "", fmt.Errorf("derive SNI: empty key material")
	}

	okm := make([]byte, 22)
	reader := hkdf.New(sha256.New, ikm, []byte{}, []byte(sniHKDFInfo))
	if _, err := io.ReadFull(reader, okm); err != nil {
		return "", fmt.Errorf("derive SNI: %w", err)
	}

	return hex.EncodeToString(okm[:16]) + "." +
		hex.EncodeToString(okm[16:21]) + "." +
		sniTLDs[int(okm[21])%len(sniTLDs)], nil
}
