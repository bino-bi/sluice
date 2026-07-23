// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	minio "github.com/minio/minio-go/v7"
	"github.com/oklog/ulid/v2"
)

// ObjectStore is the subset of *minio.Client the sink uses; a test fake
// implements it without the network.
type ObjectStore interface {
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64,
		opts minio.PutObjectOptions) (minio.UploadInfo, error)
}

// S3Sink batches audit records into newline-delimited JSON objects. It is
// a best-effort secondary sink: the file sink remains the durable,
// hash-chained record. Records accumulate in an in-memory buffer that the
// dispatcher-driven Flush uploads on a size or interval trigger; a failed
// upload keeps the batch (the buffer is the retry queue) and backs off for
// one interval. When the buffer cap is reached — S3 down, retries piling
// up — incoming records are dropped for this sink only, counted in
// sluice_audit_dropped_total{sink="s3",reason="buffer_full"}.
//
// The sink spawns no goroutines; Record buffers, Flush uploads (matching
// the FileSink design, dispatcher drives the cadence).
type S3Sink struct {
	opts     S3Options
	prefix   string
	lockMode minio.RetentionMode

	mu          sync.Mutex
	buf         []byte
	count       int
	batchStart  time.Time
	nextAttempt time.Time
	failing     bool
	closed      bool
}

// S3Options configures NewS3Sink. Store and Bucket are required.
type S3Options struct {
	Store  ObjectStore
	Bucket string
	// Prefix is the object-key prefix. Default "audit/".
	Prefix string
	// ObjectLock is "" (off), "governance", or "compliance"; when set,
	// each object is written with a retention of RetentionDays. The
	// bucket must have been created with Object Lock enabled.
	ObjectLock    string
	RetentionDays int
	// UploadInterval is the maximum age of a batch before upload (and the
	// retry backoff after a failed upload). Default 30s.
	UploadInterval time.Duration
	// UploadBytes triggers an upload once the batch reaches this size.
	// Default 1 MiB.
	UploadBytes int
	// MaxBufferBytes caps the in-memory batch; overflow drops records for
	// this sink. Default 8 MiB.
	MaxBufferBytes int
	// UploadTimeout bounds one PutObject call. Default 10s.
	UploadTimeout time.Duration

	Clock  func() time.Time
	Logger *slog.Logger
}

// NewS3Sink validates options and returns the sink. No network I/O
// happens here — reachability is probed best-effort by the caller.
func NewS3Sink(opts S3Options) (*S3Sink, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("audit: s3 sink requires a store")
	}
	if opts.Bucket == "" {
		return nil, fmt.Errorf("audit: s3 sink requires a bucket")
	}
	if opts.Prefix == "" {
		opts.Prefix = "audit/"
	} else if !strings.HasSuffix(opts.Prefix, "/") {
		opts.Prefix += "/"
	}
	var mode minio.RetentionMode
	switch opts.ObjectLock {
	case "":
	case "governance":
		mode = minio.Governance
	case "compliance":
		mode = minio.Compliance
	default:
		return nil, fmt.Errorf("audit: unknown s3 objectLock mode %q", opts.ObjectLock)
	}
	if opts.ObjectLock != "" && opts.RetentionDays <= 0 {
		return nil, fmt.Errorf("audit: s3 objectLock requires retentionDays > 0")
	}
	if opts.UploadInterval <= 0 {
		opts.UploadInterval = 30 * time.Second
	}
	if opts.UploadBytes <= 0 {
		opts.UploadBytes = 1 << 20
	}
	if opts.MaxBufferBytes <= 0 {
		opts.MaxBufferBytes = 8 << 20
	}
	if opts.UploadTimeout <= 0 {
		opts.UploadTimeout = 10 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &S3Sink{opts: opts, prefix: opts.Prefix, lockMode: mode}, nil
}

// Name implements Sink.
func (s *S3Sink) Name() string { return "s3" }

// Record implements Sink: append to the batch, never touch the network.
func (s *S3Sink) Record(_ context.Context, r *Record) error {
	line, err := MarshalLine(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if len(s.buf)+len(line) > s.opts.MaxBufferBytes {
		metricsShared().DroppedTotal.WithLabelValues(s.Name(), "buffer_full").Inc()
		return nil
	}
	if len(s.buf) == 0 {
		s.batchStart = s.opts.Clock()
	}
	s.buf = append(s.buf, line...)
	s.count++
	return nil
}

// Flush implements Sink: upload when the batch is big or old enough. The
// upload runs outside the lock so a slow endpoint never blocks Record.
func (s *S3Sink) Flush(ctx context.Context) error {
	s.mu.Lock()
	now := s.opts.Clock()
	if s.closed || len(s.buf) == 0 || now.Before(s.nextAttempt) ||
		(len(s.buf) < s.opts.UploadBytes && now.Sub(s.batchStart) < s.opts.UploadInterval) {
		s.mu.Unlock()
		return nil
	}
	batch, count := s.buf, s.count
	s.buf, s.count = nil, 0
	s.mu.Unlock()

	err := s.upload(ctx, batch, now)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// The batch IS the retry queue: put it back in front of anything
		// recorded meanwhile and back off one interval.
		s.buf = append(batch, s.buf...)
		s.count += count
		s.nextAttempt = now.Add(s.opts.UploadInterval)
		metricsShared().WriteErrors.WithLabelValues(s.Name()).Inc()
		if !s.failing {
			s.failing = true
			s.opts.Logger.Warn("audit: s3 upload failing; records are buffered for retry",
				slog.String("error", err.Error()))
		}
		return nil
	}
	s.nextAttempt = time.Time{}
	if len(s.buf) > 0 {
		s.batchStart = now
	}
	if s.failing {
		s.failing = false
		s.opts.Logger.Info("audit: s3 upload recovered")
	}
	return nil
}

// Close implements Sink: one final upload attempt for the remaining batch.
func (s *S3Sink) Close(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	batch, count := s.buf, s.count
	s.buf, s.count = nil, 0
	s.mu.Unlock()

	if len(batch) == 0 {
		return nil
	}
	if err := s.upload(ctx, batch, s.opts.Clock()); err != nil {
		metricsShared().DroppedTotal.WithLabelValues(s.Name(), "write_error").Add(float64(count))
		return fmt.Errorf("audit: s3 final upload dropped %d record(s): %w", count, err)
	}
	return nil
}

func (s *S3Sink) upload(ctx context.Context, batch []byte, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, s.opts.UploadTimeout)
	defer cancel()
	key := fmt.Sprintf("%s%s/audit-%s.jsonl", s.prefix, now.UTC().Format("2006/01/02"), ulid.Make())
	popts := minio.PutObjectOptions{ContentType: "application/x-ndjson"}
	if s.lockMode != "" {
		popts.Mode = s.lockMode
		popts.RetainUntilDate = now.UTC().AddDate(0, 0, s.opts.RetentionDays)
	}
	_, err := s.opts.Store.PutObject(ctx, s.opts.Bucket, key, bytes.NewReader(batch), int64(len(batch)), popts)
	return err
}
