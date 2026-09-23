// Command fixturegen creates disposable mTLS and JWT material for the
// official-vs-Go black-box compatibility harness. Never use it in production.
package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	out := flag.String("out", "", "directory outside the source tree for disposable private keys")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(2)
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		panic(err)
	}
	if err := generate(*out); err != nil {
		panic(err)
	}
	fmt.Println("disposable compatibility fixtures generated")
}

func generate(out string) error {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	otherJWTKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "compat-test-ca"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caParsed, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	server := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, AuthorityKeyId: caParsed.SubjectKeyId}
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	client := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "compat-test-client"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, AuthorityKeyId: caParsed.SubjectKeyId}
	clientDER, err := x509.CreateCertificate(rand.Reader, client, ca, &clientKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	jwtPublicDER, err := x509.MarshalPKIXPublicKey(&jwtKey.PublicKey)
	if err != nil {
		return err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	clientPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})
	jwtPublicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: jwtPublicDER})
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})
	for name, data := range map[string][]byte{"ca.pem": caPEM, "client.pem": clientPEM, "client.key": clientKeyPEM} {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o600); err != nil {
			return err
		}
	}
	secretJSON, err := json.Marshal(map[string]string{"caCertPem": string(caPEM), "nodeCertPem": string(serverPEM), "nodeKeyPem": string(serverKeyPEM), "jwtPublicKey": string(jwtPublicPEM)})
	if err != nil {
		return err
	}
	secret := base64.StdEncoding.EncodeToString(secretJSON)
	for name, extra := range map[string]string{
		"official.env": "",
		"go.env":       "LOG_DIR=/tmp/rnl-log\nDATA_DIR=/tmp/rnl-data\nGEO_DIR=/tmp/rnl-geo\nXRAY_BIN=/missing-rw-core\n",
	} {
		contents := "NODE_PORT=2222\nSECRET_KEY=" + secret + "\nSNI_VERIFICATION=false\n" + extra
		if err := os.WriteFile(filepath.Join(out, name), []byte(contents), 0o600); err != nil {
			return err
		}
	}
	claims := map[string]any{"iss": "remnawave", "aud": "remnawave-node", "sub": "remnawave-backend", "exp": now.Add(24 * time.Hour).Unix()}
	valid, err := signJWT(jwtKey, claims)
	if err != nil {
		return err
	}
	badSignature, err := signJWT(otherJWTKey, claims)
	if err != nil {
		return err
	}
	expired, err := signJWT(jwtKey, map[string]any{"exp": now.Add(-time.Hour).Unix()})
	if err != nil {
		return err
	}
	wrongClaims, err := signJWT(jwtKey, map[string]any{"iss": "other", "aud": "other", "sub": "other", "exp": now.Add(24 * time.Hour).Unix()})
	if err != nil {
		return err
	}
	tokens, err := json.Marshal(map[string]string{"valid": valid, "badSignature": badSignature, "expired": expired, "wrongClaims": wrongClaims})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "tokens.json"), tokens, 0o600)
}

func signJWT(key *rsa.PrivateKey, claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	part := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(part))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return part + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
