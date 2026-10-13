// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	if cfg.Logging.MaxErrorBytes != 4096 {
		t.Errorf("Logging.MaxErrorBytes = %d, want 4096", cfg.Logging.MaxErrorBytes)
	}
}

// An explicit 0 turns the cut of logged errors off. It must not fall back
// to the default.
func TestLoadServer_LoggingMaxErrorBytesZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sluice.yaml")
	if err := os.WriteFile(path, []byte("logging:\n  maxErrorBytes: 0\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServer(path, nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Logging.MaxErrorBytes != 0 {
		t.Errorf("Logging.MaxErrorBytes = %d, want 0", cfg.Logging.MaxErrorBytes)
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
	t.Setenv("SLUICE_LOGGING__MAXERRORBYTES", "0")

	cfg, err := LoadServer("", nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Logging.MaxErrorBytes != 0 {
		t.Errorf("Logging.MaxErrorBytes = %d, want 0 (via env)", cfg.Logging.MaxErrorBytes)
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

// Every scalar key is readable from the environment, also one without a
// default. SLUICE_ADMIN__TOKEN alone used to be ignored, which left the
// admin API without a token.
func TestLoadServer_EnvSetsKeyWithoutDefault(t *testing.T) {
	t.Setenv("SLUICE_ADMIN__TOKEN", "s3cret")
	t.Setenv("SLUICE_MCP__LISTEN", ":7000")
	t.Setenv("SLUICE_DUCKDB__TEMPDIR", "/var/tmp/sluice")
	t.Setenv("SLUICE_IDENTITY__APIKEYPEPPER", "secret://env/PEPPER")
	t.Setenv("SLUICE_REST__TLS__CERTFILE", "/etc/sluice/tls.crt")
	t.Setenv("SLUICE_MCP__TLS__KEYFILE", "/etc/sluice/mcp.key")
	t.Setenv("SLUICE_AUDIT__FILE__PATH", "/var/log/sluice")
	t.Setenv("SLUICE_AUDIT__S3__INSECURE", "true")
	t.Setenv("SLUICE_POLICIES__COMPOSITE__MEMBERS", "yaml,opa")
	t.Setenv("SLUICE_LIMITS__QUERYTIMEOUT", "7s")
	t.Setenv("SLUICE_LIMITS__MAXRESULTBYTES", "1024")

	cfg, err := LoadServer("", nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.Admin.Token != "s3cret" || cfg.MCP.Listen != ":7000" || cfg.DuckDB.TempDir != "/var/tmp/sluice" ||
		cfg.Identity.APIKeyPepper != "secret://env/PEPPER" {
		t.Errorf("scalar keys not read: admin.token %q, mcp.listen %q, duckdb.tempDir %q, identity.apiKeyPepper %q",
			cfg.Admin.Token, cfg.MCP.Listen, cfg.DuckDB.TempDir, cfg.Identity.APIKeyPepper)
	}
	if cfg.REST.TLS == nil || cfg.REST.TLS.CertFile != "/etc/sluice/tls.crt" {
		t.Errorf("rest.tls.certFile not read: %+v", cfg.REST.TLS)
	}
	if cfg.MCP.TLS == nil || cfg.MCP.TLS.KeyFile != "/etc/sluice/mcp.key" {
		t.Errorf("mcp.tls.keyFile not read: %+v", cfg.MCP.TLS)
	}
	if cfg.Audit.File == nil || cfg.Audit.File.Path != "/var/log/sluice" {
		t.Errorf("audit.file.path not read: %+v", cfg.Audit.File)
	}
	if cfg.Audit.S3 == nil || !cfg.Audit.S3.Insecure {
		t.Errorf("audit.s3.insecure not read: %+v", cfg.Audit.S3)
	}
	if got := strings.Join(cfg.Policies.Composite.Members, ","); got != "yaml,opa" {
		t.Errorf("policies.composite.members = %q, want yaml,opa", got)
	}
	if cfg.Limits.MaxResultBytes != 1024 {
		t.Errorf("limits.maxResultBytes = %d, want 1024", cfg.Limits.MaxResultBytes)
	}
	if def := DefaultServerConfig(); def.Limits.MaxResultBytes != 64<<20 {
		t.Errorf("default limits.maxResultBytes = %d, want 64 MiB", def.Limits.MaxResultBytes)
	}
	if cfg.Limits.QueryTimeout != 7*time.Second {
		t.Errorf("limits.queryTimeout = %v, want 7s", cfg.Limits.QueryTimeout)
	}
}

// A bound variable that is not set must not create an optional block: a
// block that exists is validated, and an empty one fails for everyone.
func TestLoadServer_NoEnvKeepsOptionalBlocksNil(t *testing.T) {
	cfg, err := LoadServer("", nil)
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if cfg.REST.TLS != nil || cfg.MCP.TLS != nil || cfg.Admin.TLS != nil || cfg.Audit.Syslog != nil || cfg.Audit.S3 != nil {
		t.Errorf("optional blocks exist without config: rest.tls %v, mcp.tls %v, admin.tls %v, audit.syslog %v, audit.s3 %v",
			cfg.REST.TLS, cfg.MCP.TLS, cfg.Admin.TLS, cfg.Audit.Syslog, cfg.Audit.S3)
	}
	want := DefaultServerConfig()
	if !reflect.DeepEqual(*cfg, want) {
		t.Errorf("config without file and environment differs from the defaults:\n got  %+v\n want %+v", *cfg, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config does not validate: %v", err)
	}
}
