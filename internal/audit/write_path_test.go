// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/bino-bi/sluice/internal/audit"
)

// counterValue reads a counter of the default registry by its labels.
func counterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	next:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue next
				}
			}
			if c := m.GetCounter(); c != nil {
				return c.GetValue()
			}
			return m.GetGauge().GetValue()
		}
	}
	return 0
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// blockSink is a secondary sink that hangs in Record, like a syslog peer
// that takes no data.
type blockSink struct{ release chan struct{} }

func (b *blockSink) Name() string { return "block" }
func (b *blockSink) Record(_ context.Context, r *audit.Record) error {
	if r.EventType != audit.EventGenesis {
		<-b.release
	}
	return nil
}
func (*blockSink) Flush(context.Context) error { return nil }
func (*blockSink) Close(context.Context) error { return nil }

// A hanging secondary sink must not stop the primary sink or Enqueue.
func TestDispatcher_SlowSecondaryDoesNotStallPrimary(t *testing.T) {
	primary := &memSink{}
	slow := &blockSink{release: make(chan struct{})}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks:           []audit.Sink{primary, slow},
		GenesisSeed:     []byte("slow-secondary"),
		QueueSize:       2,
		EnqueueDeadline: 50 * time.Millisecond,
		Logger:          testLogger(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	dropsBefore := counterValue(t, "sluice_audit_dropped_total", map[string]string{"sink": "block", "reason": "queue_full"})
	for i := range 50 {
		if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		// Let the worker take the record, so only the secondary queue fills.
		waitFor(t, "the primary sink", func() bool { return primary.count() == i+2 })
	}
	if got := counterValue(t, "sluice_audit_dropped_total", map[string]string{"sink": "block", "reason": "queue_full"}) - dropsBefore; got == 0 {
		t.Error("no drop counted for the full secondary queue")
	}
	close(slow.release)
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The queue depth gauge follows Enqueue, also while the worker hangs.
func TestDispatcher_QueueDepthAtEnqueue(t *testing.T) {
	sink := &gateSink{entered: make(chan string, 8), gate: make(chan struct{}), seed: "seedtip"}
	sink2 := &gateSinkNamed{gateSink: sink}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks: []audit.Sink{sink2}, QueueSize: 4, FlushInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"q1", "q2", "q3"} {
		if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery, QueryID: id}); err != nil {
			t.Fatal(err)
		}
	}
	<-sink.entered
	if got := counterValue(t, "sluice_audit_queue_depth", map[string]string{"sink": "gate-depth"}); got != 2 {
		t.Errorf("queue depth = %v while the worker hangs, want 2", got)
	}
	close(sink.gate)
	_ = d.Close(context.Background())
}

type gateSinkNamed struct{ *gateSink }

func (*gateSinkNamed) Name() string { return "gate-depth" }

// With a full queue one caller waits, the others fail at once. The wait
// honors the context, and the records keep the chain order.
func TestDispatcher_FullQueueFailsFast(t *testing.T) {
	sink := &gateSink{entered: make(chan string, 16), gate: make(chan struct{}), seed: "seedtip"}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks: []audit.Sink{sink}, QueueSize: 1, EnqueueDeadline: 3 * time.Second, FlushInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	enq := func(ctx context.Context, id string) error {
		return d.Enqueue(ctx, &audit.Record{EventType: audit.EventQuery, QueryID: id})
	}
	if err := enq(context.Background(), "q1"); err != nil {
		t.Fatal(err)
	}
	<-sink.entered // the worker holds q1
	if err := enq(context.Background(), "q2"); err != nil {
		t.Fatal(err)
	}

	// One caller with a context waits for space.
	ctx, cancel := context.WithCancel(context.Background())
	waiterErr := make(chan error, 1)
	go func() { waiterErr <- enq(ctx, "cancelled") }()
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	var wg sync.WaitGroup
	var full atomic.Int32
	for range 10 {
		wg.Go(func() {
			if errors.Is(enq(context.Background(), "x"), audit.ErrQueueFull) {
				full.Add(1)
			}
		})
	}
	wg.Wait()
	if took := time.Since(start); took > time.Second {
		t.Errorf("10 callers took %v with a full queue, want an answer at once", took)
	}
	if full.Load() != 10 {
		t.Errorf("ErrQueueFull for %d of 10 callers", full.Load())
	}
	cancel()
	select {
	case err := <-waiterErr:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiter err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the waiter did not see the cancel")
	}

	// A waiter gets the slot that the worker frees.
	go func() { waiterErr <- enq(context.Background(), "q3") }()
	time.Sleep(50 * time.Millisecond)
	sink.gate <- struct{}{} // q1 written, the worker takes q2
	if err := <-waiterErr; err != nil {
		t.Fatalf("waiter: %v", err)
	}
	close(sink.gate)
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var ids []string
	prior := "seedtip"
	for _, r := range sink.records {
		ids = append(ids, r.QueryID)
		if r.PriorHash != prior {
			t.Errorf("%s: prior_hash does not chain", r.QueryID)
		}
		prior = r.Hash
	}
	if strings.Join(ids, ",") != "q1,q2,q3" {
		t.Errorf("records = %v, want q1,q2,q3", ids)
	}
}

// flakySink wraps the file sink and fails every Record while broken is
// set, like a full disk.
type flakySink struct {
	*audit.FileSink
	broken atomic.Bool
}

func (f *flakySink) Record(ctx context.Context, r *audit.Record) error {
	if f.broken.Load() {
		return errors.New("no space left on device")
	}
	return f.FileSink.Record(ctx, r)
}

// While the primary sink cannot write, Enqueue fails. When it works again
// a gap record follows, and verify accepts the break at the gap record and
// reports it.
func TestDispatcher_PrimaryDownThenGapRecord(t *testing.T) {
	dir := auditDir(t)
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	sink := &flakySink{FileSink: fs}
	seed := []byte("gap-seed")
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks: []audit.Sink{sink}, GenesisSeed: seed, RecoverInterval: 10 * time.Millisecond, Logger: testLogger(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	enq := func() error {
		return d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery})
	}
	if err := enq(); err != nil {
		t.Fatal(err)
	}
	if err := d.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	sink.broken.Store(true)
	if err := enq(); err != nil {
		t.Fatalf("the record before the failure: %v", err)
	}
	// Records queued before the worker saw the failure are lost too.
	lost := 1
	waitFor(t, "Enqueue to fail", func() bool {
		err := enq()
		if err == nil {
			lost++
		}
		return errors.Is(err, audit.ErrSinkDown)
	})

	sink.broken.Store(false)
	waitFor(t, "Enqueue to work again", func() bool { return enq() == nil })
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	rep, err := audit.Verify(dir, audit.GenesisPriorHash(seed), nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(rep.Gaps) != 1 || rep.Gaps[0].Lost != lost {
		t.Fatalf("gaps = %+v, want one gap of %d record(s)", rep.Gaps, lost)
	}

	// The gap record names the record before it: removing that one is
	// still a broken chain.
	path := filepath.Join(dir, rep.Gaps[0].File)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	gapLine := rep.Gaps[0].Line - 1
	cut := append(append([]byte(nil), bytes.Join(lines[:gapLine-1], nil)...), bytes.Join(lines[gapLine:], nil)...)
	if err := os.WriteFile(path, cut, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Verify(dir, "", nil); !errors.Is(err, audit.ErrChainBroken) {
		t.Fatalf("verify after removing the record before the gap: %v, want a broken chain", err)
	}
}

// Close writes the gap record when the sink works again before the next
// retry: a restart would chain onto the last written record and hide the
// loss.
func TestDispatcher_CloseWritesGapRecord(t *testing.T) {
	dir := auditDir(t)
	fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	sink := &flakySink{FileSink: fs}
	d, err := audit.NewDispatcher(audit.DispatcherOptions{
		Sinks: []audit.Sink{sink}, GenesisSeed: []byte("close-gap"), RecoverInterval: time.Hour, Logger: testLogger(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	sink.broken.Store(true)
	if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Enqueue to fail", func() bool {
		return errors.Is(d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery}), audit.ErrSinkDown)
	})
	sink.broken.Store(false)
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rep, err := audit.Verify(dir, "", nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(rep.Gaps) != 1 {
		t.Fatalf("gaps = %+v, want one", rep.Gaps)
	}
}

// A last line without newline after a crash is moved aside, and the chain
// continues from the last complete record.
func TestFileSink_TornTailIsMovedAside(t *testing.T) {
	dir := auditDir(t)
	seed := []byte("torn-seed")
	writeRecords := func() {
		fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		d, err := audit.NewDispatcher(audit.DispatcherOptions{Sinks: []audit.Sink{fs}, GenesisSeed: seed})
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := d.Enqueue(context.Background(), &audit.Record{EventType: audit.EventQuery}); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	writeRecords()

	files, _ := filepath.Glob(filepath.Join(dir, "audit-*.jsonl"))
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	f, err := os.OpenFile(files[0], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fragment := `{"event_type":"query","prior_ha`
	if _, err := f.WriteString(fragment); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	writeRecords()

	side, _ := filepath.Glob(files[0] + ".torn-*")
	if len(side) != 1 {
		t.Fatalf("side files = %v, want one", side)
	}
	if got, _ := os.ReadFile(side[0]); string(got) != fragment {
		t.Errorf("side file = %q, want the fragment", got)
	}
	rep, err := audit.Verify(dir, audit.GenesisPriorHash(seed), nil)
	if err != nil {
		t.Fatalf("verify after the repair: %v", err)
	}
	if rep.Records != 7 {
		t.Errorf("records = %d, want 7", rep.Records)
	}
}

// A failed open on rotation keeps the sink usable: Record returns an error
// and the next record tries the open again.
func TestFileSink_FailedRotationOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory mode")
	}
	dir := auditDir(t)
	now := time.Date(2026, 4, 19, 23, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	sink, err := audit.NewFileSink(audit.FileOptions{Dir: dir, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close(context.Background()) }()
	r1 := chain(t, "p", nil)
	if err := sink.Record(context.Background(), r1); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	now = now.Add(2 * time.Hour)
	mu.Unlock()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	r2 := chain(t, r1.Hash, nil)
	for range 2 {
		if err := sink.Record(context.Background(), r2); err == nil {
			t.Fatal("Record wrote although the new file cannot be opened")
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := sink.Record(context.Background(), r2); err != nil {
		t.Fatalf("Record after the directory is writable again: %v", err)
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, filepath.Join(dir, "audit-2026-04-20.jsonl")); len(got) != 1 {
		t.Errorf("records in the new file = %d, want 1", len(got))
	}
}

// Invalid UTF-8, as a byte cut of a multi-byte rune leaves it, still
// gives a chain that verifies, and a keyed chain starts again on it.
func TestDispatcher_InvalidUTF8Verifies(t *testing.T) {
	dir := auditDir(t)
	seed := []byte("utf8-seed")
	key := []byte("0123456789abcdef0123456789abcdef")
	open := func() *audit.Dispatcher {
		fs, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		d, err := audit.NewDispatcher(audit.DispatcherOptions{Sinks: []audit.Sink{fs}, GenesisSeed: seed, ChainKey: key})
		if err != nil {
			t.Fatalf("dispatcher: %v", err)
		}
		return d
	}
	d := open()
	bad := "Gr\xc3" // the first byte of ü
	rec := &audit.Record{
		EventType:  audit.EventQuery,
		SQLSample:  "SELECT '" + bad,
		Subject:    audit.Subject{ID: bad, Groups: []string{bad}},
		Tables:     []string{bad},
		ClientMeta: map[string]string{"k" + bad: "v" + bad},
		Extras:     map[string]any{"x": bad, "nested": map[string]any{bad: []any{bad}}},
	}
	if err := d.Enqueue(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Verify(dir, audit.GenesisPriorHash(seed), key); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// The start guard recomputes the hash of the newest record.
	_ = open().Close(context.Background())
}

// A second sink on the same directory fails until the first one closes.
func TestFileSink_DirectoryLock(t *testing.T) {
	dir := auditDir(t)
	first, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	_, err = audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "audit.file.path") {
		t.Fatalf("second sink err = %v, want a lock error that names the directory", err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := audit.NewFileSink(audit.FileOptions{Dir: dir})
	if err != nil {
		t.Fatalf("after close: %v", err)
	}
	_ = second.Close(context.Background())
}

func TestFileSink_DirectoryChecks(t *testing.T) {
	base := t.TempDir()

	created := filepath.Join(base, "new", "audit")
	s, err := audit.NewFileSink(audit.FileOptions{Dir: created})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close(context.Background())
	if info, _ := os.Stat(created); info.Mode().Perm() != 0o700 {
		t.Errorf("new directory mode = %v, want 0700", info.Mode().Perm())
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(created, link); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewFileSink(audit.FileOptions{Dir: link}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink err = %v", err)
	}

	open := filepath.Join(base, "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.NewFileSink(audit.FileOptions{Dir: open}); err == nil || !strings.Contains(err.Error(), "can write") {
		t.Errorf("group writable err = %v", err)
	}
}
