// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/transport/rest"
	"github.com/bino-bi/sluice/internal/transport/tlsutil"
	"github.com/bino-bi/sluice/internal/transport/tlsutil/tlstest"
)

// startMTLSServer serves the REST handler behind the exact tls.Config the
// transport builds from its TLSConfig, so the handshake behavior under
// test is the production one.
func startMTLSServer(t *testing.T, serverCA, clientCA *tlstest.CA) *httptest.Server {
	t.Helper()
	cert, key := serverCA.ServerCert(t)
	tc, err := tlsutil.Build(tlsutil.Config{CertFile: cert, KeyFile: key, ClientCA: clientCA.CertPath})
	if err != nil {
		t.Fatalf("build tls config: %v", err)
	}
	srv := rest.New(rest.Config{Listen: ":0"}, rest.Deps{
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.TLS = tc
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

func TestMTLS_ValidClientCertServed(t *testing.T) {
	t.Parallel()
	serverCA := tlstest.NewCA(t, "server")
	clientCA := tlstest.NewCA(t, "clients")
	ts := startMTLSServer(t, serverCA, clientCA)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      serverCA.Pool(t),
		Certificates: []tls.Certificate{clientCA.ClientCert(t)},
		MinVersion:   tls.VersionTLS12,
	}}}
	resp, err := client.Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatalf("request with valid client cert: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
}

func TestMTLS_NoClientCertRejected(t *testing.T) {
	t.Parallel()
	serverCA := tlstest.NewCA(t, "server")
	clientCA := tlstest.NewCA(t, "clients")
	ts := startMTLSServer(t, serverCA, clientCA)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    serverCA.Pool(t),
		MinVersion: tls.VersionTLS12,
	}}}
	if _, err := client.Get(ts.URL + "/v1/health"); err == nil {
		t.Fatal("expected handshake failure without a client certificate")
	}
}

func TestMTLS_WrongCAClientCertRejected(t *testing.T) {
	t.Parallel()
	serverCA := tlstest.NewCA(t, "server")
	clientCA := tlstest.NewCA(t, "clients")
	rogueCA := tlstest.NewCA(t, "rogue")
	ts := startMTLSServer(t, serverCA, clientCA)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:      serverCA.Pool(t),
		Certificates: []tls.Certificate{rogueCA.ClientCert(t)},
		MinVersion:   tls.VersionTLS12,
	}}}
	if _, err := client.Get(ts.URL + "/v1/health"); err == nil {
		t.Fatal("expected handshake failure for a cert from the wrong CA")
	}
}

func TestListenAndServe_BadClientCAFailsBeforeBind(t *testing.T) {
	t.Parallel()
	ca := tlstest.NewCA(t, "server")
	cert, key := ca.ServerCert(t)
	srv := rest.New(rest.Config{
		Listen: "127.0.0.1:0",
		TLS:    &rest.TLSConfig{CertFile: cert, KeyFile: key, ClientCA: "/nonexistent/ca.pem"},
	}, rest.Deps{
		Identifier: &stubIdentifier{user: &identity.UserCtx{Subject: "x"}},
	})
	if err := srv.ListenAndServe(context.Background()); err == nil {
		t.Fatal("expected error for unreadable clientCA")
	}
}
