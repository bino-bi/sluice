// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolver_Env(t *testing.T) {
	t.Setenv("SLUICE_TEST_SECRET", "hunter2")

	r := NewResolver(ResolverOptions{})
	got, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "hunter2" {
		t.Fatalf("value = %q", got)
	}
}

func TestResolver_EnvMissing(t *testing.T) {
	r := NewResolver(ResolverOptions{})
	_, err := r.Resolve(context.Background(), "secret://env/SLUICE_DEFINITELY_NOT_SET_xyz")
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestResolver_UnsupportedProvider(t *testing.T) {
	r := NewResolver(ResolverOptions{})
	_, err := r.Resolve(context.Background(), "secret://vault/secret/data/x")
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("want unsupported-provider error, got %v", err)
	}
}

func TestResolver_ResolveString_TrimsWhitespace(t *testing.T) {
	t.Setenv("SLUICE_TEST_SECRET_NL", "hunter2\n")
	r := NewResolver(ResolverOptions{})
	got, err := r.ResolveString(context.Background(), "secret://env/SLUICE_TEST_SECRET_NL")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "hunter2" {
		t.Fatalf("value = %q, want %q", got, "hunter2")
	}
}

func TestResolver_File_OK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := NewResolver(ResolverOptions{})
	uri := "secret://file/" + path // URL path becomes absolute since path is absolute
	got, err := r.Resolve(context.Background(), uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "file-secret" {
		t.Fatalf("value = %q", got)
	}
}

func TestResolver_File_RejectsWorldWritable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("x"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	r := NewResolver(ResolverOptions{})
	_, err := r.Resolve(context.Background(), "secret://file/"+path)
	if err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("expected world-writable rejection, got %v", err)
	}
}

func TestResolver_File_WarnsWorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("x"), 0o604); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0o604); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := NewResolver(ResolverOptions{Logger: logger})

	_, err := r.Resolve(context.Background(), "secret://file/"+path)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(buf.String(), "world-readable") {
		t.Fatalf("expected warning for world-readable file; log was: %s", buf.String())
	}
}

func TestResolver_CachingAndInvalidate(t *testing.T) {
	t.Setenv("SLUICE_TEST_SECRET_C", "v1")
	r := NewResolver(ResolverOptions{TTL: time.Minute})

	got1, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET_C")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	t.Setenv("SLUICE_TEST_SECRET_C", "v2")

	// Cache should still return v1.
	got2, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET_C")
	if err != nil {
		t.Fatalf("cached resolve: %v", err)
	}
	if string(got2) != string(got1) {
		t.Fatalf("cache did not return first value: first=%q second=%q", got1, got2)
	}

	r.Invalidate()
	got3, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET_C")
	if err != nil {
		t.Fatalf("post-invalidate resolve: %v", err)
	}
	if string(got3) != "v2" {
		t.Fatalf("post-invalidate value = %q, want %q", got3, "v2")
	}
}

func TestResolver_RedactionInLogs(t *testing.T) {
	t.Setenv("SLUICE_TEST_SECRET_R", "hunter2")

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	r := NewResolver(ResolverOptions{Logger: logger})

	got, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET_R")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(got) != "hunter2" {
		t.Fatalf("value = %q", got)
	}
	if strings.Contains(buf.String(), "hunter2") {
		t.Fatalf("secret leaked into logs: %s", buf.String())
	}
}

func TestResolver_CacheIndependence(t *testing.T) {
	t.Setenv("SLUICE_TEST_SECRET_I", "abcdef")
	r := NewResolver(ResolverOptions{})

	first, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET_I")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Mutating the returned slice must not poison the cache.
	first[0] = 'X'

	second, err := r.Resolve(context.Background(), "secret://env/SLUICE_TEST_SECRET_I")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(second) != "abcdef" {
		t.Fatalf("cache contaminated by caller mutation: %q", second)
	}
}
