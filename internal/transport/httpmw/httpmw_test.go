// SPDX-License-Identifier: AGPL-3.0-or-later

package httpmw

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestBodyCap(t *testing.T) {
	var readErr error
	h := BodyCap(4)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345")))
	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Fatalf("read error = %v, want *http.MaxBytesError", readErr)
	}
}

func TestRequestID(t *testing.T) {
	var got string
	h := RequestID("X-Request-Id")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "abc")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if got != "abc" || w.Header().Get("X-Request-Id") != "abc" {
		t.Fatalf("context id %q, header %q, want abc", got, w.Header().Get("X-Request-Id"))
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if got == "" || got != w.Header().Get("X-Request-Id") {
		t.Fatalf("new id: context %q, header %q", got, w.Header().Get("X-Request-Id"))
	}
}

func panicCount(t *testing.T) float64 {
	t.Helper()
	var m dto.Metric
	if err := panicsMetric().WithLabelValues("test").Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func TestRecover(t *testing.T) {
	before := panicCount(t)
	h := Recover("test", discard(), func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if got := panicCount(t); got != before+1 {
		t.Fatalf("panics counter = %v, want %v", got, before+1)
	}
}

func TestRecover_AbortHandlerPanicsAgain(t *testing.T) {
	h := Recover("test", discard(), func(http.ResponseWriter) {
		t.Error("an abort must not write a response")
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		if err, _ := recover().(error); !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want http.ErrAbortHandler", err)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

type countLimiter struct{ left int }

func (c *countLimiter) Allow(string) bool {
	if c.left == 0 {
		return false
	}
	c.left--
	return true
}

func TestRateLimit_PerIPFirst(t *testing.T) {
	perIP := &countLimiter{left: 0}
	global := &countLimiter{left: 1}
	var scope string
	h := RateLimit(perIP, global, func(_ http.ResponseWriter, _ *http.Request, s string) { scope = s })(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler must not run") }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if scope != "per_ip" {
		t.Fatalf("scope = %q, want per_ip", scope)
	}
	if global.left != 1 {
		t.Fatal("a per-IP refusal must not take a global token")
	}
}

func TestRateLimit_NilLimitersAllow(t *testing.T) {
	ran := false
	h := RateLimit(nil, nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !ran {
		t.Fatal("handler did not run without limiters")
	}
}

// A request that outlives the drain timeout has its context cancelled,
// and Shutdown returns only after its handler did.
func TestDrain_SlowHandler(t *testing.T) {
	d := NewDrain()
	started := make(chan struct{})
	var returned, sawCancel atomic.Bool
	srv := &http.Server{ReadHeaderTimeout: time.Second, BaseContext: d.BaseContext}
	srv.Handler = d.Track(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		sawCancel.Store(true)
		// Work after the cancel, as a query that writes its audit record.
		time.Sleep(50 * time.Millisecond)
		returned.Store(true)
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started

	begin := time.Now()
	d.Shutdown(context.Background(), srv, 100*time.Millisecond, discard(), "test")
	if !sawCancel.Load() || !returned.Load() {
		t.Fatalf("Shutdown returned before the handler: cancelled=%v returned=%v", sawCancel.Load(), returned.Load())
	}
	if el := time.Since(begin); el > 2*time.Second {
		t.Fatalf("Shutdown took %v", el)
	}
}

func TestDrain_IdleServer(t *testing.T) {
	d := NewDrain()
	srv := &http.Server{ReadHeaderTimeout: time.Second, BaseContext: d.BaseContext, Handler: d.Track(http.NotFoundHandler())}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	d.Shutdown(context.Background(), srv, time.Second, discard(), "test")
	if d.ctx.Err() == nil {
		t.Fatal("base context must be cancelled after Shutdown")
	}
}
