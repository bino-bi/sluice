// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bino-bi/sluice/internal/version"
)

// DispatcherOptions configures a new Dispatcher.
type DispatcherOptions struct {
	// Sinks receive every enqueued record. The first sink in the slice is
	// the "primary" — its LastHash (if any) is used to seed the chain.
	Sinks []Sink

	// QueueSize bounds the buffered channel. Default 10_000.
	QueueSize int

	// EnqueueDeadline caps how long Enqueue blocks before returning
	// ErrQueueFull. Default 200 ms.
	EnqueueDeadline time.Duration

	// FlushInterval spawns a periodic flusher goroutine. Default 200 ms.
	// Set to zero to disable automatic flushing.
	FlushInterval time.Duration

	// DrainTimeout caps how long Close waits for the queue to drain.
	// Default 10s.
	DrainTimeout time.Duration

	// GenesisSeed initialises the chain when no existing sink has a
	// LastHash (first boot in a fresh directory). Required — the
	// resolver caller provides a secret://-sourced value.
	GenesisSeed []byte

	// Origin names the installation (e.g. hostname); embedded in the
	// genesis record for operator diagnostics.
	Origin string

	Logger *slog.Logger
	Clock  func() time.Time
}

// Dispatcher owns a bounded queue, a background worker that drains into
// sinks, and a periodic flusher. A single Dispatcher is shared across
// transports; each call to Enqueue serialises through the queue.
type Dispatcher struct {
	opts   DispatcherOptions
	queue  chan *Record
	quit   chan struct{}
	doneWg sync.WaitGroup
	// pending tracks records that have been enqueued but not yet handed
	// to a sink. Flush waits on this to make "drained" a real property
	// of the sinks, not just of the queue channel.
	pending sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	lastHash string
	metrics  *metricsSet
}

// NewDispatcher builds a Dispatcher, emits the genesis record if needed,
// and starts the background goroutines.
func NewDispatcher(opts DispatcherOptions) (*Dispatcher, error) {
	if len(opts.Sinks) == 0 {
		return nil, fmt.Errorf("audit: at least one sink required")
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 10_000
	}
	if opts.EnqueueDeadline <= 0 {
		opts.EnqueueDeadline = 200 * time.Millisecond
	}
	if opts.FlushInterval < 0 {
		opts.FlushInterval = 0
	} else if opts.FlushInterval == 0 {
		opts.FlushInterval = 200 * time.Millisecond
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = 10 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}

	d := &Dispatcher{
		opts:    opts,
		queue:   make(chan *Record, opts.QueueSize),
		quit:    make(chan struct{}),
		metrics: metricsShared(),
	}

	// Seed the chain: prefer the primary sink's LastHash; otherwise write
	// a genesis record.
	seed := primaryLastHash(opts.Sinks)
	if seed == "" {
		if len(opts.GenesisSeed) == 0 {
			return nil, fmt.Errorf("audit: GenesisSeed required on fresh chain")
		}
		gen := newGenesisRecord(opts)
		gen.PriorHash = GenesisPriorHash(opts.GenesisSeed)
		h, err := ComputeHash(gen.PriorHash, gen)
		if err != nil {
			return nil, err
		}
		gen.Hash = h
		for _, s := range opts.Sinks {
			if err := s.Record(context.Background(), gen); err != nil {
				return nil, fmt.Errorf("audit: genesis write %s: %w", s.Name(), err)
			}
		}
		for _, s := range opts.Sinks {
			if err := s.Flush(context.Background()); err != nil {
				return nil, fmt.Errorf("audit: genesis flush %s: %w", s.Name(), err)
			}
		}
		d.lastHash = gen.Hash
	} else {
		d.lastHash = seed
	}

	d.doneWg.Add(1)
	go d.run()
	if d.opts.FlushInterval > 0 {
		d.doneWg.Add(1)
		go d.flushLoop()
	}
	return d, nil
}

// primaryLastHash reads LastHash off the first sink that supports it.
func primaryLastHash(sinks []Sink) string {
	for _, s := range sinks {
		if h, ok := s.(interface{ LastHash() string }); ok {
			if got := h.LastHash(); got != "" {
				return got
			}
		}
	}
	return ""
}

// newGenesisRecord constructs the EventGenesis record written at first
// boot. The record identifies the sluice build and origin so auditors can
// detect genesis events from unexpected installations.
func newGenesisRecord(opts DispatcherOptions) *Record {
	b := version.Current()
	return &Record{
		Timestamp:     opts.Clock().UTC(),
		EventType:     EventGenesis,
		SluiceVersion: b.Version,
		ParserVersion: b.Parser,
		Message:       "audit chain initialised",
		Extras: map[string]any{
			"origin": opts.Origin,
		},
	}
}

// Enqueue accepts a partially-populated record. The dispatcher finalises
// PriorHash + Hash + Timestamp + SluiceVersion before handing it to
// sinks. r may be mutated; callers must not reuse the same pointer.
func (d *Dispatcher) Enqueue(ctx context.Context, r *Record) error {
	if r == nil {
		return fmt.Errorf("audit: nil record")
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return ErrClosed
	}
	// Finalise inside the lock so records are appended in strict chain
	// order. Sink writes happen on the worker goroutine so we don't hold
	// the mutex across I/O.
	if r.Timestamp.IsZero() {
		r.Timestamp = d.opts.Clock().UTC()
	}
	if r.SluiceVersion == "" {
		r.SluiceVersion = version.Current().Version
	}
	r.PriorHash = d.lastHash
	h, err := ComputeHash(d.lastHash, r)
	if err != nil {
		d.mu.Unlock()
		return err
	}
	r.Hash = h
	d.lastHash = h
	d.mu.Unlock()

	d.metrics.Enqueued.WithLabelValues(string(r.EventType)).Inc()
	d.pending.Add(1)

	// Non-blocking fast path.
	select {
	case d.queue <- r:
		return nil
	default:
	}

	// Bounded blocking path.
	timer := time.NewTimer(d.opts.EnqueueDeadline)
	defer timer.Stop()
	select {
	case d.queue <- r:
		return nil
	case <-ctx.Done():
		d.pending.Done()
		d.recordDrop("context")
		return ctx.Err()
	case <-timer.C:
		d.pending.Done()
		d.recordDrop("queue_full")
		return ErrQueueFull
	}
}

func (d *Dispatcher) recordDrop(reason string) {
	for _, s := range d.opts.Sinks {
		d.metrics.DroppedTotal.WithLabelValues(s.Name(), reason).Inc()
	}
}

// run is the single writer goroutine. It preserves chain order because
// channel sends preserve insertion order.
func (d *Dispatcher) run() {
	defer d.doneWg.Done()
	for {
		select {
		case r, ok := <-d.queue:
			if !ok {
				return
			}
			d.dispatch(r)
		case <-d.quit:
			// Drain anything that remains in the queue before exiting.
			for {
				select {
				case r := <-d.queue:
					d.dispatch(r)
				default:
					return
				}
			}
		}
	}
}

func (d *Dispatcher) dispatch(r *Record) {
	defer d.pending.Done()
	for _, s := range d.opts.Sinks {
		if err := s.Record(context.Background(), r); err != nil {
			d.metrics.WriteErrors.WithLabelValues(s.Name()).Inc()
			d.opts.Logger.Error("audit: sink write failed",
				slog.String("sink", s.Name()),
				slog.String("event_type", string(r.EventType)),
				slog.String("error", err.Error()),
			)
		}
	}
	for _, s := range d.opts.Sinks {
		d.metrics.QueueDepth.WithLabelValues(s.Name()).Set(float64(len(d.queue)))
	}
}

// flushLoop invokes Flush on every sink periodically.
func (d *Dispatcher) flushLoop() {
	defer d.doneWg.Done()
	t := time.NewTicker(d.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.flushAll()
		case <-d.quit:
			return
		}
	}
}

func (d *Dispatcher) flushAll() {
	ctx, cancel := context.WithTimeout(context.Background(), d.opts.DrainTimeout)
	defer cancel()
	for _, s := range d.opts.Sinks {
		if err := s.Flush(ctx); err != nil {
			d.metrics.WriteErrors.WithLabelValues(s.Name()).Inc()
			d.opts.Logger.Error("audit: sink flush failed",
				slog.String("sink", s.Name()),
				slog.String("error", err.Error()),
			)
		}
	}
}

// Flush forces every sink to persist pending data. It waits until the
// queue has drained.
func (d *Dispatcher) Flush(ctx context.Context) error {
	if err := d.drain(ctx); err != nil {
		return err
	}
	return d.flushWithCtx(ctx)
}

// drain blocks until every in-flight record has been dispatched to its
// sinks, or ctx is done. This is stronger than "queue channel is empty":
// the worker consumes from the channel before writing to sinks, so an
// empty queue does not imply an empty sink write queue.
func (d *Dispatcher) drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		d.pending.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) flushWithCtx(ctx context.Context) error {
	var firstErr error
	for _, s := range d.opts.Sinks {
		if err := s.Flush(ctx); err != nil {
			d.metrics.WriteErrors.WithLabelValues(s.Name()).Inc()
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Close signals the worker to exit, drains the queue, and closes every
// sink. It waits up to DrainTimeout.
func (d *Dispatcher) Close(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	d.mu.Unlock()

	close(d.quit)

	// Wait for goroutines.
	done := make(chan struct{})
	go func() {
		d.doneWg.Wait()
		close(done)
	}()
	drainCtx, cancel := context.WithTimeout(ctx, d.opts.DrainTimeout)
	defer cancel()
	select {
	case <-done:
	case <-drainCtx.Done():
		d.opts.Logger.Warn("audit: drain timeout on close")
	}

	var firstErr error
	for _, s := range d.opts.Sinks {
		if err := s.Close(context.Background()); err != nil {
			d.metrics.WriteErrors.WithLabelValues(s.Name()).Inc()
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// LastHash exposes the current chain tip. Used by tests; production code
// should rely on the dispatcher as the only writer.
func (d *Dispatcher) LastHash() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastHash
}
