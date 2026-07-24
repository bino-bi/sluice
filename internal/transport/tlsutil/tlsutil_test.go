// SPDX-License-Identifier: AGPL-3.0-or-later

package tlsutil_test

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/internal/transport/tlsutil"
	"github.com/bino-bi/sluice/internal/transport/tlsutil/tlstest"
)

func TestBuild_ServerOnly(t *testing.T) {
	t.Parallel()
	ca := tlstest.NewCA(t, "server")
	cert, key := ca.ServerCert(t)

	tc, err := tlsutil.Build(tlsutil.Config{CertFile: cert, KeyFile: key})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tc.ClientAuth != tls.NoClientCert {
		t.Fatalf("ClientAuth = %v; want NoClientCert", tc.ClientAuth)
	}
	if tc.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x; want TLS 1.2", tc.MinVersion)
	}
	if len(tc.Certificates) != 1 {
		t.Fatalf("Certificates = %d; want 1", len(tc.Certificates))
	}
}

func TestBuild_ClientCAEnablesMutualTLS(t *testing.T) {
	t.Parallel()
	serverCA := tlstest.NewCA(t, "server")
	clientCA := tlstest.NewCA(t, "clients")
	cert, key := serverCA.ServerCert(t)

	tc, err := tlsutil.Build(tlsutil.Config{CertFile: cert, KeyFile: key, ClientCA: clientCA.CertPath})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if tc.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v; want RequireAndVerifyClientCert", tc.ClientAuth)
	}
	if tc.ClientCAs == nil {
		t.Fatal("ClientCAs pool not set")
	}
}

func TestBuild_MissingKeyFile(t *testing.T) {
	t.Parallel()
	ca := tlstest.NewCA(t, "server")
	cert, _ := ca.ServerCert(t)
	if _, err := tlsutil.Build(tlsutil.Config{CertFile: cert, KeyFile: "/nonexistent/key.pem"}); err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestBuild_ClientCAWithoutPEM(t *testing.T) {
	t.Parallel()
	ca := tlstest.NewCA(t, "server")
	cert, key := ca.ServerCert(t)
	junk := filepath.Join(t.TempDir(), "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	if _, err := tlsutil.Build(tlsutil.Config{CertFile: cert, KeyFile: key, ClientCA: junk}); err == nil {
		t.Fatal("expected error for PEM-free clientCA")
	}
}
