// SPDX-License-Identifier: AGPL-3.0-or-later

package datasource_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
	"github.com/bino-bi/sluice/pkg/datasource/testfakes"
)

// healthErrs dials a per-catalog probe result for the fake_health_test
// driver. Tests must use unique catalog names to stay independent.
var healthErrs sync.Map // name -> error

const fakeHealthType = "fake_health_test"

func init() {
	pkgds.Register(fakeHealthType, func(_ context.Context, spec pkgds.Spec) (pkgds.DataSource, error) {
		name := spec.Name
		return testfakes.New(name, pkgds.Schema{Catalog: name},
			testfakes.WithHealthHook(func(context.Context) error {
				if v, ok := healthErrs.Load(name); ok && v != nil {
					return v.(error)
				}
				return nil
			})), nil
	})
}

func healthSpec(name string) *apitypes.DataSource {
	return &apitypes.DataSource{
		TypeMeta: apitypes.TypeMeta{APIVersion: "sluice.dev/v1alpha1", Kind: apitypes.KindDataSource},
		Metadata: apitypes.ObjectMeta{Name: name},
		Spec:     apitypes.DataSourceSpec{Type: apitypes.DataSourceType(fakeHealthType)},
	}
}

// fakePool satisfies datasource.ConnProvider; the returned conn also
// satisfies SQLConn with a nil *sql.Conn (the testfakes driver never
// touches it).
type fakePool struct{ err error }

func (p fakePool) Conn(context.Context) (datasource.ConnCloser, error) {
	if p.err != nil {
		return nil, p.err
	}
	return fakePoolConn{}, nil
}

type fakePoolConn struct{}

func (fakePoolConn) Close() error       { return nil }
func (fakePoolConn) SQLConn() *sql.Conn { return nil }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSetPoolTriggersImmediateSweep(t *testing.T) {
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{healthSpec("hs-immediate")}},
		HealthInterval: -1, // no ticker: only SetPool's one-shot sweep runs
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	if r.Statuses()[0].Healthy {
		t.Fatal("Healthy must start false (fail-closed) before any probe")
	}
	r.SetPool(context.Background(), fakePool{})
	waitFor(t, "immediate sweep to flip Healthy", func() bool {
		return r.Statuses()[0].Healthy
	})
}

func TestProbeRecordsTransitions(t *testing.T) {
	const name = "hs-transitions"
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{healthSpec(name)}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	pool := fakePool{}
	if err := r.Probe(context.Background(), name, pool); err != nil {
		t.Fatalf("first Probe: %v", err)
	}
	if s := r.Statuses()[0]; !s.Healthy || s.LastError != "" || s.LastCheck.IsZero() {
		t.Fatalf("after healthy probe: %+v", s)
	}

	healthErrs.Store(name, errors.New("synthetic outage"))
	defer healthErrs.Delete(name)
	if err := r.Probe(context.Background(), name, pool); err == nil {
		t.Fatal("expected probe error during outage")
	}
	if s := r.Statuses()[0]; s.Healthy || s.LastError == "" {
		t.Fatalf("after failing probe: %+v", s)
	}

	healthErrs.Delete(name)
	if err := r.Probe(context.Background(), name, pool); err != nil {
		t.Fatalf("recovery Probe: %v", err)
	}
	if s := r.Statuses()[0]; !s.Healthy || s.LastError != "" {
		t.Fatalf("after recovery probe: %+v", s)
	}
}

func TestSweepSkipsWithoutPool(t *testing.T) {
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{healthSpec("hs-nopool")}},
		HealthInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	// Let several ticks pass; without a pool the sweep must not touch
	// the status (fail-closed Healthy=false, LastCheck untouched).
	time.Sleep(50 * time.Millisecond)
	if s := r.Statuses()[0]; s.Healthy || !s.LastCheck.IsZero() {
		t.Fatalf("nil-pool sweep must be a no-op, got %+v", s)
	}
}

func TestSetPoolThenImmediateClose(t *testing.T) {
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{healthSpec("hs-close")}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The one-shot sweep goroutine must tolerate a racing Close.
	r.SetPool(context.Background(), fakePool{})
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestProbePoolConnError(t *testing.T) {
	const name = "hs-connerr"
	r, err := datasource.New(context.Background(), datasource.Options{
		Snapshot:       &datasource.Snapshot{DataSources: []*apitypes.DataSource{healthSpec(name)}},
		HealthInterval: -1,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := r.Probe(context.Background(), name, fakePool{err: errors.New("pool exhausted")}); err == nil {
		t.Fatal("expected error when the pool cannot hand out a conn")
	}
	if r.Statuses()[0].Healthy {
		t.Fatal("Healthy must stay false when the pool fails")
	}
}
