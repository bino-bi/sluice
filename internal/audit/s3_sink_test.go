// SPDX-License-Identifier: AGPL-3.0-or-later

package audit_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	minio "github.com/minio/minio-go/v7"

	"github.com/bino-bi/sluice/internal/audit"
)

// fakeStore records PutObject calls; err (when set) fails every upload.
type fakeStore struct {
	mu      sync.Mutex
	err     error
	objects map[string][]byte
	locks   map[string]minio.PutObjectOptions
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}, locks: map[string]minio.PutObjectOptions{}}
}

func (f *fakeStore) PutObject(_ context.Context, _, key string, r io.Reader, _ int64,
	opts minio.PutObjectOptions,
) (minio.UploadInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return minio.UploadInfo{}, f.err
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	f.objects[key] = body
	f.locks[key] = opts
	return minio.UploadInfo{Key: key}, nil
}

func (f *fakeStore) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeStore) snapshot() map[string][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string][]byte, len(f.objects))
	maps.Copy(out, f.objects)
	return out
}

func newS3Sink(t *testing.T, store *fakeStore, clock func() time.Time, mutate func(*audit.S3Options)) *audit.S3Sink {
	t.Helper()
	opts := audit.S3Options{
		Store:          store,
		Bucket:         "audit-bucket",
		Prefix:         "audit/",
		UploadInterval: 30 * time.Second,
		UploadBytes:    1 << 20,
		Clock:          clock,
		Logger:         testLogger(t),
	}
	if mutate != nil {
		mutate(&opts)
	}
	sink, err := audit.NewS3Sink(opts)
	if err != nil {
		t.Fatalf("NewS3Sink: %v", err)
	}
	return sink
}

func TestS3Sink_BatchesAndUploadsOnInterval(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := newFakeStore()
	sink := newS3Sink(t, store, clock, nil)

	rec := &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow, Hash: "h1"}
	if err := sink.Record(context.Background(), rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// Young and small: no upload yet.
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(store.snapshot()) != 0 {
		t.Fatal("batch must not upload before the interval elapses")
	}

	now = now.Add(31 * time.Second)
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	objs := store.snapshot()
	if len(objs) != 1 {
		t.Fatalf("objects = %d, want 1", len(objs))
	}
	for key, body := range objs {
		if !strings.HasPrefix(key, "audit/2026/07/23/audit-") || !strings.HasSuffix(key, ".jsonl") {
			t.Fatalf("object key layout wrong: %q", key)
		}
		if !strings.Contains(string(body), `"hash":"h1"`) || !strings.HasSuffix(string(body), "\n") {
			t.Fatalf("object body must be the JSONL line(s): %q", body)
		}
	}
}

func TestS3Sink_FailedUploadRetriesWithoutLoss(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := newFakeStore()
	store.setErr(errors.New("s3 down"))
	sink := newS3Sink(t, store, clock, nil)

	if err := sink.Record(context.Background(), &audit.Record{EventType: audit.EventQuery, Hash: "h1"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	now = now.Add(31 * time.Second)
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("a failed upload must not surface: %v", err)
	}
	// Record another line while S3 is down; backoff holds the batch.
	if err := sink.Record(context.Background(), &audit.Record{EventType: audit.EventQuery, Hash: "h2"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	now = now.Add(time.Second)
	_ = sink.Flush(context.Background()) // inside backoff window: no attempt
	if len(store.snapshot()) != 0 {
		t.Fatal("no upload may succeed while the store errors")
	}

	store.setErr(nil)
	now = now.Add(31 * time.Second)
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	objs := store.snapshot()
	if len(objs) != 1 {
		t.Fatalf("objects = %d, want 1 (single batch with both records)", len(objs))
	}
	for _, body := range objs {
		s := string(body)
		h1 := strings.Index(s, `"hash":"h1"`)
		h2 := strings.Index(s, `"hash":"h2"`)
		if h1 < 0 || h2 < 0 || h1 > h2 {
			t.Fatalf("batch must retain both records in order: %q", s)
		}
	}
}

func TestS3Sink_BufferCapDropsOverflow(t *testing.T) {
	now := time.Unix(0, 0)
	store := newFakeStore()
	store.setErr(errors.New("s3 down"))
	sink := newS3Sink(t, store, func() time.Time { return now }, func(o *audit.S3Options) {
		o.MaxBufferBytes = 256
	})

	rec := &audit.Record{EventType: audit.EventQuery, Decision: audit.DecisionAllow, Hash: "hash-value"}
	for range 100 {
		if err := sink.Record(context.Background(), rec); err != nil {
			t.Fatalf("overflow must drop silently for this sink, got: %v", err)
		}
	}
}

func TestS3Sink_ObjectLockHeaders(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	store := newFakeStore()
	sink := newS3Sink(t, store, clock, func(o *audit.S3Options) {
		o.ObjectLock = "compliance"
		o.RetentionDays = 90
	})

	if err := sink.Record(context.Background(), &audit.Record{EventType: audit.EventQuery}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for key, opts := range store.locks {
		if opts.Mode != minio.Compliance {
			t.Fatalf("object %q lock mode = %q, want COMPLIANCE", key, opts.Mode)
		}
		want := now.AddDate(0, 0, 90)
		if !opts.RetainUntilDate.Equal(want) {
			t.Fatalf("retainUntil = %v, want %v", opts.RetainUntilDate, want)
		}
	}
	if len(store.locks) != 1 {
		t.Fatalf("uploads = %d, want 1", len(store.locks))
	}
}

func TestS3Sink_CloseDrainsAndRejectsFurtherRecords(t *testing.T) {
	store := newFakeStore()
	sink := newS3Sink(t, store, nil, nil)

	if err := sink.Record(context.Background(), &audit.Record{EventType: audit.EventQuery, Hash: "h"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(store.snapshot()) != 1 {
		t.Fatal("Close must upload the remaining batch")
	}
	if err := sink.Record(context.Background(), &audit.Record{}); err != audit.ErrClosed {
		t.Fatalf("Record after Close = %v, want ErrClosed", err)
	}
}
