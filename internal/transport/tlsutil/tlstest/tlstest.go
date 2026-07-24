// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlstest generates throwaway certificate authorities and leaf
// certificates for transport tests. Test-only helper: never import from
// production code.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CA is an in-memory certificate authority whose PEM lives on disk for
// config fields that take file paths.
type CA struct {
	CertPath string
	cert     *x509.Certificate
	key      *ecdsa.PrivateKey
}

// NewCA creates a self-signed CA and writes <dir>/<name>-ca.pem.
func NewCA(t *testing.T, name string) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("tlstest: generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("tlstest: create CA cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("tlstest: parse CA cert: %v", err)
	}
	path := filepath.Join(t.TempDir(), name+"-ca.pem")
	writePEM(t, path, "CERTIFICATE", der)
	return &CA{CertPath: path, cert: cert, key: key}
}

// ServerCert issues a localhost server certificate signed by ca and
// returns the PEM file paths (cert, key).
func (ca *CA) ServerCert(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	der, keyDER := ca.issue(t, x509.ExtKeyUsageServerAuth)
	dir := t.TempDir()
	certPath = filepath.Join(dir, "server.pem")
	keyPath = filepath.Join(dir, "server-key.pem")
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)
	return certPath, keyPath
}

// ClientCert issues a client certificate signed by ca, ready for
// tls.Config.Certificates.
func (ca *CA) ClientCert(t *testing.T) tls.Certificate {
	t.Helper()
	der, keyDER := ca.issue(t, x509.ExtKeyUsageClientAuth)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("tlstest: client keypair: %v", err)
	}
	return cert
}

// Pool returns a cert pool containing only this CA.
func (ca *CA) Pool(t *testing.T) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

func (ca *CA) issue(t *testing.T, usage x509.ExtKeyUsage) (certDER, keyDER []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("tlstest: generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("tlstest: issue leaf: %v", err)
	}
	keyDER, err = x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("tlstest: marshal leaf key: %v", err)
	}
	return der, keyDER
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("tlstest: open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("tlstest: encode %s: %v", path, err)
	}
}
