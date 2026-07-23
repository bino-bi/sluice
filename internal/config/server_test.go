// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadServer_Defaults_NoFile(t *testing.T) {
	cfg, err := LoadServer("", nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.REST.Listen != ":8080" {
		t.Errorf("REST.Listen = %q, want :8080", cfg.REST.Listen)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("Logging.Format = %q", cfg.Logging.Format)
	}
	if cfg.Limits.MaxRows != 100_000 {
		t.Errorf("Limits.MaxRows = %d", cfg.Limits.MaxRows)
	}
	if cfg.Audit.SQLSampleBytes != 2048 {
		t.Errorf("Audit.SQLSampleBytes = %d, want 2048", cfg.Audit.SQLSampleBytes)
	}
}

func TestLoadServer_AuditSQLSampleBytesOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sluice.yaml")
	if err := os.WriteFile(path, []byte("audit:\n  sqlSampleBytes: 512\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServer(path, nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Audit.SQLSampleBytes != 512 {
		t.Errorf("Audit.SQLSampleBytes = %d, want 512", cfg.Audit.SQLSampleBytes)
	}
}

func TestLoadServer_MissingFile_NotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	cfg, err := LoadServer(path, nil)
	if err != nil {
		t.Fatalf("missing file should not be an error: %v", err)
	}
	if cfg.REST.Listen != ":8080" {
		t.Errorf("defaults lost: %+v", cfg.REST)
	}
}

func TestLoadServer_YAMLOverrides(t *testing.T) {
	cfg, err := LoadServer("testdata/server/minimal.yaml", nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.REST.Listen != ":8081" {
		t.Errorf("Listen = %q", cfg.REST.Listen)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("Format = %q", cfg.Logging.Format)
	}
	if cfg.Limits.MaxRows != 5000 {
		t.Errorf("MaxRows = %d", cfg.Limits.MaxRows)
	}
	if cfg.Limits.QueryTimeout != 15*time.Second {
		t.Errorf("QueryTimeout = %v", cfg.Limits.QueryTimeout)
	}
	// Defaults still applied for unmentioned fields.
	if cfg.DuckDB.MemoryLimit != "4GB" {
		t.Errorf("DuckDB.MemoryLimit = %q", cfg.DuckDB.MemoryLimit)
	}
}

func TestLoadServer_EnvOverride(t *testing.T) {
	t.Setenv("SLUICE_REST__LISTEN", ":9000")
	t.Setenv("SLUICE_LIMITS__MAXROWS", "42")

	cfg, err := LoadServer("", nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.REST.Listen != ":9000" {
		t.Errorf("REST.Listen = %q, want :9000 (via env)", cfg.REST.Listen)
	}
	if cfg.Limits.MaxRows != 42 {
		t.Errorf("MaxRows = %d, want 42 (via env)", cfg.Limits.MaxRows)
	}
}

func TestLoadServer_RejectsMalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	// Viper needs something structurally wrong to reject. Unterminated flow
	// mapping triggers the yaml parser's hard error.
	if err := os.WriteFile(path, []byte("rest: {listen: :8080\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadServer(path, nil); err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}
