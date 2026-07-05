// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchOptions configures a Watcher.
type WatchOptions struct {
	// Dir is the policy/data-source manifest directory. Required.
	Dir string

	// Registry receives the new Snapshot on every successful reload.
	// Required.
	Registry *Registry

	// Load performs the actual directory read + validation. Defaults to
	// the MVP loader (LoadDirectory); injectable for tests.
	Load func(ctx context.Context, opts LoadOptions) (*Snapshot, error)

	// Debounce collapses a burst of filesystem events into a single
	// reload. Default 250 ms; plan doc §6.
	Debounce time.Duration

	// Strict is forwarded to LoadDirectory.
	Strict bool

	// Logger receives slog output. Nil uses slog.Default.
	Logger *slog.Logger
}

// Watcher reloads the policy manifest directory whenever files change and
// publishes the new Snapshot through the Registry. Triggered on fsnotify
// events, SIGHUP (wired from cmd/sluice), or the admin /admin/reload
// endpoint (via the Reload method).
type Watcher struct {
	opts WatchOptions
	log  *slog.Logger

	fsw *fsnotify.Watcher

	stop    chan struct{}
	stopped chan struct{}

	started atomic.Bool

	// reloadMu serialises Load+Publish so a fsnotify-triggered reload and a
	// manual Reload (SIGHUP / admin) cannot run concurrently and publish
	// snapshots out of order.
	reloadMu sync.Mutex

	mu          sync.Mutex
	lastApplied time.Time
}

// NewWatcher returns a Watcher ready to Start. It installs fsnotify on
// opts.Dir but does not block.
func NewWatcher(opts WatchOptions) (*Watcher, error) {
	if opts.Dir == "" {
		return nil, errors.New("config: WatchOptions.Dir required")
	}
	if opts.Registry == nil {
		return nil, errors.New("config: WatchOptions.Registry required")
	}
	if opts.Load == nil {
		opts.Load = LoadDirectory
	}
	if opts.Debounce <= 0 {
		opts.Debounce = 250 * time.Millisecond
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("config: fsnotify: %w", err)
	}
	if err := addDirRecursive(fsw, opts.Dir); err != nil {
		_ = fsw.Close()
		return nil, err
	}

	return &Watcher{
		opts:    opts,
		log:     opts.Logger,
		fsw:     fsw,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}, nil
}

// Start begins consuming fsnotify events and reloading on Dir changes.
// Returns immediately; the watcher runs until ctx is cancelled or Close
// is called. Calling Start more than once is a no-op.
func (w *Watcher) Start(ctx context.Context) {
	if !w.started.CompareAndSwap(false, true) {
		return
	}
	go w.run(ctx)
}

// Reload forces a non-debounced reload, blocking until the new snapshot
// is published or the loader returns an error. Invoked by SIGHUP and the
// admin /admin/reload handler.
func (w *Watcher) Reload(ctx context.Context) error {
	return w.reloadOnce(ctx)
}

// Close stops the watcher and releases the fsnotify handles. Safe to
// call multiple times and before Start.
func (w *Watcher) Close() error {
	select {
	case <-w.stop:
		return nil
	default:
		close(w.stop)
	}
	// Only wait for the run goroutine to exit when it was actually
	// started; otherwise stopped is never closed.
	if w.started.Load() {
		<-w.stopped
	}
	return w.fsw.Close()
}

func (w *Watcher) run(ctx context.Context) {
	defer close(w.stopped)
	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if isRelevant(ev) {
				pending = true
				// Reset to debounce window; bursty writes collapse into
				// one reload.
				if !debounce.Stop() {
					select {
					case <-debounce.C:
					default:
					}
				}
				debounce.Reset(w.opts.Debounce)
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.log.WarnContext(ctx, "config watch: fsnotify error",
				slog.String("error", err.Error()))
		case <-debounce.C:
			if pending {
				pending = false
				if err := w.reloadOnce(ctx); err != nil {
					w.log.WarnContext(ctx, "config watch: reload failed",
						slog.String("error", err.Error()))
				}
			}
		}
	}
}

func (w *Watcher) reloadOnce(ctx context.Context) error {
	// Serialise Load+Publish so concurrent reload triggers apply in a
	// well-defined order (last to run reads the freshest directory state).
	w.reloadMu.Lock()
	defer w.reloadMu.Unlock()
	snap, err := w.opts.Load(ctx, LoadOptions{
		Strict:  w.opts.Strict,
		Sources: []SourceDir{{Path: w.opts.Dir}},
	})
	if err != nil {
		return err
	}
	w.opts.Registry.Publish(snap)
	w.mu.Lock()
	w.lastApplied = time.Now()
	w.mu.Unlock()
	w.log.InfoContext(ctx, "config watch: reload applied",
		slog.Int("objects", len(snap.Policies)),
		slog.String("digest", snap.Digest),
	)
	return nil
}

// isRelevant filters out events that shouldn't trigger a reload — e.g.,
// transient editor swap files. Only YAML / YML names matter.
func isRelevant(ev fsnotify.Event) bool {
	name := filepath.Base(ev.Name)
	if strings.HasPrefix(name, ".") {
		return false
	}
	low := strings.ToLower(name)
	if !strings.HasSuffix(low, ".yaml") && !strings.HasSuffix(low, ".yml") {
		return false
	}
	return ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) != 0
}

// addDirRecursive registers dir and every sub-directory with fsnotify.
// Single-level is sufficient for MVP but recursing is cheap and lets
// operators drop new sub-directories without restarting.
func addDirRecursive(fsw *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Missing directory is not fatal — the operator may not have
			// created the policies.d yet. Watcher will fire as soon as
			// files appear once we return without error.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		return fsw.Add(path)
	})
}
