// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bino-bi/sluice/internal/ratelimit"
	"github.com/bino-bi/sluice/internal/version"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// Bounds of the auth-event records of one origin, in records per second
// and as a burst. A flood of failed logins must not fill the audit log.
const (
	AuthEventRate  = 5
	AuthEventBurst = 20
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

	// ChainKey, when set, keys the hash of every new record (HashAlgHMAC).
	// At least MinChainKeyBytes long. Empty keeps the plain chain.
	ChainKey []byte

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

	authEvents *ratelimit.KeyedLimiter
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

	if n := len(opts.ChainKey); n > 0 && n < MinChainKeyBytes {
		return nil, fmt.Errorf("audit: the chain key has %d bytes, it needs at least %d", n, MinChainKeyBytes)
	}
	if err := checkChainKey(opts.Sinks[0], opts.ChainKey); err != nil {
		return nil, err
	}

	d := &Dispatcher{
		opts:    opts,
		queue:   make(chan *Record, opts.QueueSize),
		quit:    make(chan struct{}),
		metrics: metricsShared(),
		authEvents: ratelimit.NewKeyed(
			ratelimit.Spec{RPS: AuthEventRate, Burst: AuthEventBurst}, 8, opts.Clock),
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
		h, err := sealRecord(opts.ChainKey, gen.PriorHash, gen)
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

// sealRecord computes the hash of a new record, keyed when key is set.
func sealRecord(key []byte, prior string, r *Record) (string, error) {
	if len(key) > 0 {
		r.HashAlg = HashAlgHMAC
	}
	return chainHash(key, prior, r)
}

// checkChainKey refuses a start that would break a keyed chain: the
// newest record is keyed and the key is missing or another one. A plain
// chain may get a key: the first keyed record chains onto the last plain
// hash.
func checkChainKey(primary Sink, key []byte) error {
	lr, ok := primary.(interface{ LastRecord() *Record })
	if !ok {
		return nil
	}
	last := lr.LastRecord()
	if last == nil {
		return nil
	}
	if last.HashAlg == "" {
		if len(key) > 0 {
			// Visible on purpose: this happens once per directory. A
			// second time means that someone removed the key marks.
			slog.Default().Warn("audit: the newest record has no key, the chain is keyed from here on")
		}
		return nil
	}
	if len(key) == 0 {
		return errors.New("audit: the newest record is keyed, set audit.chainKeyRef to the key of this audit directory")
	}
	h, err := chainHash(key, last.PriorHash, last)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(h), []byte(last.Hash)) {
		return errors.New("audit: the newest record does not verify with audit.chainKeyRef: another key, or the record was changed")
	}
	return nil
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
	return d.enqueue(ctx, r, true)
}

// AuthFailure writes an auth-event record for a request that failed the
// login. origin names the transport, reason is one of the fixed texts of
// identity.FailureReason. The record holds no part of the credential.
//
// A flood of failed logins must not fill the audit log or hold up the
// query records, and the 401 must not wait for the audit path. So each
// origin has its own rate limit, and an event is dropped at once when the
// queue is full or another record holds the dispatcher. A dropped event
// is counted.
func (d *Dispatcher) AuthFailure(origin, remoteIP, reason string) {
	if d.authEvents.Allow(origin) {
		err := d.enqueue(context.Background(), &Record{
			EventType: EventAuthEvent,
			Origin:    origin,
			RemoteIP:  remoteIP,
			Decision:  DecisionError,
			ErrorCode: string(pkgerr.CodeUnauthorized),
			Message:   reason,
		}, false)
		if err == nil {
			return
		}
	}
	d.metrics.AuthEventsDropped.WithLabelValues(origin).Inc()
}

// enqueue is Enqueue. With wait false it gives up at once when the queue
// is full or the lock is held, and counts no drop. A record that waits
// for the queue holds the lock for up to EnqueueDeadline.
func (d *Dispatcher) enqueue(ctx context.Context, r *Record, wait bool) error {
	if r == nil {
		return fmt.Errorf("audit: nil record")
	}
	// The whole operation is serialised under mu so records enter the queue
	// in strict chain order AND so the chain tip (lastHash) advances only
	// once the record is safely queued. A dropped record therefore never
	// leaves a gap: the next record chains onto the same prior hash. In
	// normal operation the non-blocking send succeeds immediately, so mu is
	// held only briefly; it is held across the bounded wait only when the
	// queue is already full (i.e. the gateway is overloaded), which is
	// exactly when serialising enqueues is acceptable. Sink I/O still runs
	// off-lock on the worker goroutine.
	if wait {
		d.mu.Lock()
	} else if !d.mu.TryLock() {
		return ErrQueueFull
	}
	defer d.mu.Unlock()
	if d.closed {
		return ErrClosed
	}
	if r.Timestamp.IsZero() {
		r.Timestamp = d.opts.Clock().UTC()
	}
	if r.SluiceVersion == "" {
		r.SluiceVersion = version.Current().Version
	}
	r.PriorHash = d.lastHash
	h, err := sealRecord(d.opts.ChainKey, d.lastHash, r)
	if err != nil {
		return err
	}
	r.Hash = h

	d.pending.Add(1)

	// Non-blocking fast path.
	select {
	case d.queue <- r:
		d.lastHash = h
		d.metrics.Enqueued.WithLabelValues(string(r.EventType)).Inc()
		return nil
	default:
	}
	if !wait {
		d.pending.Done()
		return ErrQueueFull
	}

	// Bounded blocking path.
	timer := time.NewTimer(d.opts.EnqueueDeadline)
	defer timer.Stop()
	select {
	case d.queue <- r:
		d.lastHash = h
		d.metrics.Enqueued.WithLabelValues(string(r.EventType)).Inc()
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
