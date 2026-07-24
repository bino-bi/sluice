// SPDX-License-Identifier: AGPL-3.0-or-later

package admin_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bino-bi/sluice/internal/transport/admin"
	"github.com/bino-bi/sluice/internal/transport/tlsutil"
	"github.com/bino-bi/sluice/internal/transport/tlsutil/tlstest"
)

func TestAdminMTLS_HandshakeGatesBearer(t *testing.T) {
	t.Parallel()
	serverCA := tlstest.NewCA(t, "server")
	clientCA := tlstest.NewCA(t, "clients")
	cert, key := serverCA.ServerCert(t)
	tc, err := tlsutil.Build(tlsutil.Config{CertFile: cert, KeyFile: key, ClientCA: clientCA.CertPath})
	if err != nil {
		t.Fatalf("build tls config: %v", err)
	}

	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: "s3cret"}, admin.Deps{})
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.TLS = tc
	ts.StartTLS()
	t.Cleanup(ts.Close)

	// Without a client certificate the handshake fails before auth.
	bare := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    serverCA.Pool(t),
		MinVersion: tls.VersionTLS12,
	}}}
	if _, err := bare.Get(ts.URL + "/admin/version"); err == nil {
		t.Fatal("expected handshake failure without a client certificate")
	}

	// With a client certificate the bearer token still applies.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      serverCA.Pool(t),
		Certificates: []tls.Certificate{clientCA.ClientCert(t)},
		MinVersion:   tls.VersionTLS12,
	}}}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/version", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request with valid client cert: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
}
