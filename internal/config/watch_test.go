// SPDX-License-Identifier: AGPL-3.0-or-later

package config_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/config"
)

// TestWatcher_ReloadOnChange writes a policy file, starts the watcher, then
// rewrites the file and asserts the registry receives a new snapshot.
func TestWatcher_ReloadOnChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writePolicy := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writePolicy("allow.yaml", minimalAllowYAML)

	reg := config.NewRegistry()
	var published atomic.Int64
	reg.Subscribe(func(_, cur *config.Snapshot) {
		if cur != nil {
			published.Add(1)
		}
	})

	// Prime the registry before the watcher starts so the first publish we
	// see is triggered by a file-system event, not the initial load.
	snap, err := config.LoadDirectory(context.Background(), config.LoadOptions{
		Sources: []config.SourceDir{{Path: dir}},
	})
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	reg.Publish(snap)
	initial := published.Load()

	w, err := config.NewWatcher(config.WatchOptions{
		Dir:      dir,
		Registry: reg,
		Debounce: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w.Start(ctx)

	// Touch the file — the resulting Write event should trigger a reload.
	writePolicy("allow.yaml", minimalAllowYAML)

	deadline := time.After(2 * time.Second)
	for {
		if published.Load() > initial {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("watcher did not republish (published=%d, initial=%d)",
				published.Load(), initial)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestWatcher_ManualReload is the admin /admin/reload + SIGHUP hot path:
// no filesystem change, just a direct Reload call.
func TestWatcher_ManualReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "allow.yaml"), []byte(minimalAllowYAML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := config.NewRegistry()
	var published atomic.Int64
	reg.Subscribe(func(_, cur *config.Snapshot) {
		if cur != nil {
			published.Add(1)
		}
	})

	w, err := config.NewWatcher(config.WatchOptions{
		Dir:      dir,
		Registry: reg,
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if err := w.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if published.Load() != 1 {
		t.Fatalf("expected 1 publish, got %d", published.Load())
	}
}

func TestWatcher_ValidateRejectsPublish(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "allow.yaml"), []byte(minimalAllowYAML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := config.NewRegistry()
	var published atomic.Int64
	reg.Subscribe(func(_, cur *config.Snapshot) {
		if cur != nil {
			published.Add(1)
		}
	})

	sentinel := errors.New("compile failed")
	w, err := config.NewWatcher(config.WatchOptions{
		Dir:      dir,
		Registry: reg,
		Validate: func(context.Context, *config.Snapshot) error {
			return fmt.Errorf("policy: %w", sentinel)
		},
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	rerr := w.Reload(context.Background())
	if !errors.Is(rerr, sentinel) {
		t.Fatalf("reload err = %v; want wrapped sentinel", rerr)
	}
	if published.Load() != 0 {
		t.Fatalf("invalid snapshot must not publish; got %d publishes", published.Load())
	}
}

func TestWatcher_ValidateAcceptsPublish(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "allow.yaml"), []byte(minimalAllowYAML), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reg := config.NewRegistry()
	var published atomic.Int64
	reg.Subscribe(func(_, cur *config.Snapshot) {
		if cur != nil {
			published.Add(1)
		}
	})

	w, err := config.NewWatcher(config.WatchOptions{
		Dir:      dir,
		Registry: reg,
		Validate: func(context.Context, *config.Snapshot) error { return nil },
	})
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if err := w.Reload(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if published.Load() != 1 {
		t.Fatalf("expected 1 publish, got %d", published.Load())
	}
}

const minimalAllowYAML = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: allow-analytics
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["analytics"]
        resources:
          catalogs: ["warehouse"]
`
