// SPDX-License-Identifier: AGPL-3.0-or-later

package rest_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/transport/rest"
)

// blockingIdentifier holds the request until its context ends, then
// works a little longer, as a query that writes its audit record.
type blockingIdentifier struct {
	started  chan struct{}
	returned chan struct{}
}

func (b *blockingIdentifier) Identify(ctx context.Context, _ *http.Request) (*identity.UserCtx, error) {
	close(b.started)
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
	close(b.returned)
	return nil, identity.ErrInvalidCredential
}
func (b *blockingIdentifier) Name() string { return "blocking" }

// A request that outlives the drain has its context cancelled, and
// ListenAndServe returns nil only after its handler did, so the caller
// can close audit and DuckDB.
func TestListenAndServe_DrainCancelsSlowRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	id := &blockingIdentifier{started: make(chan struct{}), returned: make(chan struct{})}
	srv := rest.New(rest.Config{Listen: addr, ShutdownTimeout: 100 * time.Millisecond}, rest.Deps{Identifier: id})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	go func() {
		for range 50 {
			resp, err := http.Post("http://"+addr+"/v1/query", "application/json", strings.NewReader(`{"sql":"SELECT 1"}`))
			if err == nil {
				_ = resp.Body.Close()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	<-id.started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe = %v, want nil after a drain timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return")
	}
	select {
	case <-id.returned:
	default:
		t.Fatal("ListenAndServe returned before the handler")
	}
}
