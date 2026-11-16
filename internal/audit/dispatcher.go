// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
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
	// the primary: its LastHash (if any) is used to seed the chain, and
	// Enqueue fails while it cannot write. The other sinks get copies
	// through their own queue, which drops records when it is full.
	Sinks []Sink

	// QueueSize bounds the buffered channel, and the queue of each
	// secondary sink. Default 10_000.
	QueueSize int

	// EnqueueDeadline caps how long Enqueue blocks before returning
	// ErrQueueFull. Default 200 ms.
	EnqueueDeadline time.Duration

	// FlushInterval spawns a periodic flusher goroutine. Default 200 ms.
	// Set to zero to disable automatic flushing.
	FlushInterval time.Duration

	// RecoverInterval is how often the dispatcher tries the primary sink
	// again after a failed write. Default 1 s.
	RecoverInterval time.Duration

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
// the primary sink, one goroutine with its own queue per secondary sink,
// and a periodic flusher. So a slow secondary sink never holds up the
// primary sink or Enqueue. A single Dispatcher is shared across
// transports; each call to Enqueue serialises through the queue.
type Dispatcher struct {
	opts        DispatcherOptions
	primary     Sink
	secondaries []*secondary
	queue       chan *Record
	quit        chan struct{}
	doneWg      sync.WaitGroup
	secWg       sync.WaitGroup
	// pending tracks records that have been enqueued but not yet handed
	// to a sink. Flush waits on this to make "drained" a real property
	// of the sinks, not just of the queue channel.
	pending atomic.Int64
	// space gets a token when the worker takes a record from the queue.
	space chan struct{}
	// waitSlot admits one Enqueue at a time to wait for a full queue.
	waitSlot chan struct{}

	mu       sync.Mutex
	closed   bool
	lastHash string
	metrics  *metricsSet

	// down is set while the primary sink cannot write. lost counts the
	// sealed records it did not get since then. lost is owned by the
	// worker goroutine.
	down atomic.Bool
	lost int

	authEvents *ratelimit.KeyedLimiter
}

// secondary is a best-effort sink with its own queue and goroutine.
type secondary struct {
	sink  Sink
	queue chan *Record
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
	if opts.RecoverInterval <= 0 {
		opts.RecoverInterval = time.Second
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
		opts:     opts,
		primary:  opts.Sinks[0],
		queue:    make(chan *Record, opts.QueueSize),
		quit:     make(chan struct{}),
		space:    make(chan struct{}, 1),
		waitSlot: make(chan struct{}, 1),
		metrics:  metricsShared(),
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

	for _, s := range opts.Sinks[1:] {
		sec := &secondary{sink: s, queue: make(chan *Record, opts.QueueSize)}
		d.secondaries = append(d.secondaries, sec)
		d.secWg.Add(1)
		go d.runSecondary(sec)
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
	sanitizeRecord(r)
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
// is full or the lock is held, and counts no drop.
//
// Records enter the queue in strict chain order, and the chain tip
// (lastHash) advances only once a record is queued: seal, send and tip
// update run under mu. A dropped record therefore never leaves a gap. mu
// is never held across a wait. When the queue is full, one caller waits up
// to EnqueueDeadline for space and seals again on each try. Every other
// caller fails at once, so an audit stall gives fast errors, not a line of
// callers that wait one after the other.
func (d *Dispatcher) enqueue(ctx context.Context, r *Record, wait bool) error {
	if r == nil {
		return fmt.Errorf("audit: nil record")
	}
	if wait {
		d.mu.Lock()
	} else if !d.mu.TryLock() {
		return ErrQueueFull
	}
	sent, err := d.trySendLocked(r)
	d.mu.Unlock()
	if sent || !wait {
		if err == nil && !sent {
			err = ErrQueueFull
		}
		return err
	}
	if err != nil {
		return d.refused(err)
	}

	select {
	case d.waitSlot <- struct{}{}:
		defer func() { <-d.waitSlot }()
	default:
		d.recordDrop("queue_full")
		return ErrQueueFull
	}
	timer := time.NewTimer(d.opts.EnqueueDeadline)
	defer timer.Stop()
	for {
		select {
		case <-d.space:
		case <-ctx.Done():
			d.recordDrop("context")
			return ctx.Err()
		case <-timer.C:
			d.recordDrop("queue_full")
			return ErrQueueFull
		}
		d.mu.Lock()
		sent, err := d.trySendLocked(r)
		d.mu.Unlock()
		if err != nil {
			return d.refused(err)
		}
		if sent {
			return nil
		}
	}
}

// refused counts a record that Enqueue refused because the primary sink
// is down, and returns err.
func (d *Dispatcher) refused(err error) error {
	if errors.Is(err, ErrSinkDown) {
		d.recordDrop("sink_down")
	}
	return err
}

// trySendLocked seals r onto the chain tip and queues it without waiting.
// It reports false when the queue is full. Must be called with d.mu held.
func (d *Dispatcher) trySendLocked(r *Record) (bool, error) {
	if d.closed {
		return false, ErrClosed
	}
	if d.down.Load() {
		return false, ErrSinkDown
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
		return false, err
	}
	r.Hash = h

	d.pending.Add(1)
	select {
	case d.queue <- r:
		d.lastHash = h
		d.metrics.Enqueued.WithLabelValues(string(r.EventType)).Inc()
		d.metrics.QueueDepth.WithLabelValues(d.primary.Name()).Set(float64(len(d.queue)))
		return true, nil
	default:
		d.pending.Add(-1)
		return false, nil
	}
}

func (d *Dispatcher) recordDrop(reason string) {
	for _, s := range d.opts.Sinks {
		d.metrics.DroppedTotal.WithLabelValues(s.Name(), reason).Inc()
	}
}

// run is the single writer goroutine of the primary sink. It preserves
// chain order because channel sends preserve insertion order. It is the
// only sender on the secondary queues and closes them when it ends.
func (d *Dispatcher) run() {
	defer d.doneWg.Done()
	defer func() {
		for _, s := range d.secondaries {
			close(s.queue)
		}
	}()
	retry := time.NewTicker(d.opts.RecoverInterval)
	defer retry.Stop()
	for {
		select {
		case r := <-d.queue:
			d.dispatch(r)
		case <-retry.C:
			if d.down.Load() {
				d.recover()
			}
		case <-d.quit:
			// Drain anything that remains in the queue before exiting.
			for {
				select {
				case r := <-d.queue:
					d.dispatch(r)
				default:
					// A restart chains onto the last written record, so
					// only a gap record written now marks the loss.
					if d.down.Load() {
						d.recover()
					}
					return
				}
			}
		}
	}
}

// dispatch writes r to the primary sink and hands copies to the
// secondary queues.
func (d *Dispatcher) dispatch(r *Record) {
	defer d.pending.Add(-1)
	select {
	case d.space <- struct{}{}:
	default:
	}
	name := d.primary.Name()
	d.metrics.QueueDepth.WithLabelValues(name).Set(float64(len(d.queue)))
	if d.down.Load() {
		// r chains onto a record that the primary sink did not get. The
		// gap record covers it.
		d.lost++
		d.metrics.DroppedTotal.WithLabelValues(name, "sink_down").Inc()
	} else if err := d.primary.Record(context.Background(), r); err != nil {
		d.lost++
		d.down.Store(true)
		d.metrics.WriteErrors.WithLabelValues(name).Inc()
		d.metrics.DroppedTotal.WithLabelValues(name, "write_error").Inc()
		d.opts.Logger.Error("audit: the primary sink cannot write, new records fail until it works again",
			slog.String("sink", name),
			slog.String("event_type", string(r.EventType)),
			slog.String("error", err.Error()),
		)
	}
	d.offer(r)
}

// recover writes a gap record once the primary sink works again. The gap
// record chains onto the last sealed record and names the last record the
// sink wrote, so verify can tell the lost records from a removal. It runs
// on the worker goroutine.
func (d *Dispatcher) recover() {
	// While down no record is sealed or queued, so the tip is stable and
	// the write runs without mu: a hanging disk must not block Enqueue.
	d.mu.Lock()
	// The records still in the queue chain onto lost records.
	for drained := false; !drained; {
		select {
		case r := <-d.queue:
			d.dispatch(r)
		default:
			drained = true
		}
	}
	tip := d.lastHash
	d.mu.Unlock()

	lastWritten := ""
	if lh, ok := d.primary.(interface{ LastHash() string }); ok {
		lastWritten = lh.LastHash()
	}
	gap := &Record{
		Timestamp:     d.opts.Clock().UTC(),
		EventType:     EventAuditGap,
		SluiceVersion: version.Current().Version,
		Message:       "audit records lost, the audit file could not be written",
		Extras: map[string]any{
			GapLostRecords: d.lost,
			GapLastWritten: lastWritten,
		},
		PriorHash: tip,
	}
	h, err := sealRecord(d.opts.ChainKey, tip, gap)
	if err != nil {
		return
	}
	gap.Hash = h
	if err := d.primary.Record(context.Background(), gap); err != nil {
		return
	}
	d.opts.Logger.Warn("audit: the primary sink works again, wrote a gap record",
		slog.String("sink", d.primary.Name()), slog.Int("lost_records", d.lost))
	d.mu.Lock()
	d.lastHash = h
	d.lost = 0
	d.down.Store(false)
	d.mu.Unlock()
	d.offer(gap)
}

// offer queues a copy of r for each secondary sink. A full queue drops the
// copy for that sink only.
func (d *Dispatcher) offer(r *Record) {
	for _, s := range d.secondaries {
		d.pending.Add(1)
		select {
		case s.queue <- r:
			d.metrics.QueueDepth.WithLabelValues(s.sink.Name()).Set(float64(len(s.queue)))
		default:
			d.pending.Add(-1)
			d.metrics.DroppedTotal.WithLabelValues(s.sink.Name(), "queue_full").Inc()
		}
	}
}

// runSecondary writes and flushes one secondary sink until its queue is
// closed.
func (d *Dispatcher) runSecondary(s *secondary) {
	defer d.secWg.Done()
	var tick <-chan time.Time
	if d.opts.FlushInterval > 0 {
		t := time.NewTicker(d.opts.FlushInterval)
		defer t.Stop()
		tick = t.C
	}
	for {
		select {
		case r, ok := <-s.queue:
			if !ok {
				return
			}
			d.metrics.QueueDepth.WithLabelValues(s.sink.Name()).Set(float64(len(s.queue)))
			if err := s.sink.Record(context.Background(), r); err != nil {
				d.metrics.WriteErrors.WithLabelValues(s.sink.Name()).Inc()
				d.opts.Logger.Error("audit: sink write failed",
					slog.String("sink", s.sink.Name()),
					slog.String("event_type", string(r.EventType)),
					slog.String("error", err.Error()),
				)
			}
			d.pending.Add(-1)
		case <-tick:
			d.flushSink(s.sink)
		}
	}
}

// flushLoop flushes the primary sink periodically. Secondary sinks flush
// on their own goroutines, so a slow upload does not delay the fsync.
func (d *Dispatcher) flushLoop() {
	defer d.doneWg.Done()
	t := time.NewTicker(d.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.flushSink(d.primary)
		case <-d.quit:
			return
		}
	}
}

func (d *Dispatcher) flushSink(s Sink) {
	ctx, cancel := context.WithTimeout(context.Background(), d.opts.DrainTimeout)
	defer cancel()
	if err := s.Flush(ctx); err != nil {
		d.metrics.WriteErrors.WithLabelValues(s.Name()).Inc()
		d.opts.Logger.Error("audit: sink flush failed",
			slog.String("sink", s.Name()),
			slog.String("error", err.Error()),
		)
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
	// A counter and not a WaitGroup: the worker adds records from zero
	// while Flush may wait, which a WaitGroup does not allow.
	t := time.NewTicker(time.Millisecond)
	defer t.Stop()
	for d.pending.Load() > 0 {
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
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
		d.secWg.Wait()
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
