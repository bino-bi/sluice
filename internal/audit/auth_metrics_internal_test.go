// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

type nullSink struct{}

func (nullSink) Name() string                          { return "null" }
func (nullSink) Record(context.Context, *Record) error { return nil }
func (nullSink) Flush(context.Context) error           { return nil }
func (nullSink) Close(context.Context) error           { return nil }

// A failed login without a record is counted for its origin.
func TestAuthFailure_DroppedEventsAreCounted(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	d, err := NewDispatcher(DispatcherOptions{
		Sinks: []Sink{nullSink{}}, GenesisSeed: []byte("m"), Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close(context.Background()) }()
	read := func() float64 {
		var m dto.Metric
		if err := d.metrics.AuthEventsDropped.WithLabelValues("metric-test").Write(&m); err != nil {
			t.Fatal(err)
		}
		return m.GetCounter().GetValue()
	}
	before := read()
	for range AuthEventBurst + 7 {
		d.AuthFailure("metric-test", "203.0.113.7", "no credential")
	}
	if got := read() - before; got != 7 {
		t.Errorf("dropped = %v, want 7", got)
	}
}
