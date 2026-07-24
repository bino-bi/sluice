// SPDX-License-Identifier: AGPL-3.0-or-later

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// Config holds TLS material paths shared by the REST and admin listeners.
type Config struct {
	CertFile string
	KeyFile  string
	// ClientCA, when set, enables mutual TLS: clients must present a
	// certificate signed by this CA (RequireAndVerifyClientCert).
	ClientCA string
}

// Build loads the server cert/key and optional client CA into a
// *tls.Config (MinVersion TLS 1.2). Unreadable or unparseable files
// return an error so the caller can refuse to start.
func Build(c Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("tlsutil: load cert/key: %w", err)
	}
	tc := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if c.ClientCA != "" {
		pem, err := os.ReadFile(c.ClientCA)
		if err != nil {
			return nil, fmt.Errorf("tlsutil: read clientCA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tlsutil: clientCA %s contains no PEM certificates", c.ClientCA)
		}
		tc.ClientCAs = pool
		tc.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return tc, nil
}
