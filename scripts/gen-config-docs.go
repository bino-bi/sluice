// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build ignore

// gen-config-docs renders docs/reference/configuration.md from
// internal/config. Keys come from reflection on ServerConfig, defaults
// from DefaultServerConfig(). The meaning of each key is written by hand
// below: a key without one fails the run.
// Run via `make docs-generate`; `make docs-check` fails when the page
// differs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/config"
)

// skip lists keys that the code declares but never uses. walk sets an
// entry to false when it meets the key.
var skip = map[string]bool{
	"datasources.directory": true, // Validate rejects it: DataSource objects load from policies.directory
	"datasources.reload":    true, // Validate rejects true
}

// blockDefault holds the value the code uses when a key of an optional
// block is empty (internal/audit/syslog_sink.go, s3_sink.go,
// cmd/sluice/auditwire.go, internal/approval/webhook.go). The blocks are
// nil in DefaultServerConfig.
var blockDefault = map[string]string{
	"audit.syslog.network":        "udp",
	"audit.syslog.facility":       "local0",
	"audit.syslog.tag":            "sluice",
	"audit.s3.endpoint":           "s3.amazonaws.com",
	"audit.s3.prefix":             "audit/",
	"audit.s3.uploadInterval":     "30s",
	"audit.s3.uploadBytes":        "1048576",
	"audit.s3.maxBufferBytes":     "8388608",
	"approval.webhooks[].timeout": "10s",
}

// meaning holds one short description per key.
var meaning = map[string]string{
	"rest.listen":         "REST API address.",
	"rest.tls.certFile":   "Certificate file. Needs `keyFile`.",
	"rest.tls.keyFile":    "Key file. Needs `certFile`.",
	"rest.tls.clientCA":   "CA file. Clients must send a certificate.",
	"rest.tls.clientAuth": "Only `require_and_verify`. Needs `clientCA`.",
	"rest.maxBodyBytes":   "Largest request body.",
	"rest.requestTimeout": "Time limit of a request.",

	"mcp.enabled":        "Start MCP in `sluice serve`.",
	"mcp.transport":      "`stdio` or `streamable_http`.",
	"mcp.listen":         "Address for `streamable_http`. Required for it.",
	"mcp.tls.certFile":   "Certificate file. Needs `keyFile`.",
	"mcp.tls.keyFile":    "Key file. Needs `certFile`.",
	"mcp.tls.clientCA":   "CA file. Clients must send a certificate.",
	"mcp.tls.clientAuth": "Only `require_and_verify`. Needs `clientCA`.",
	"mcp.sessionIdleMax": "HTTP idle timeout.",
	"mcp.tokenRef":       "Secret reference: JWT for `stdio`.",
	"mcp.apiKeyRef":      "Secret reference: API key for `stdio`.",
	"mcp.allowAnonymous": "Accept clients without a credential.",

	"admin.enabled":        "Start the admin API.",
	"admin.listen":         "Admin API address.",
	"admin.token":          "Bearer token, as plain text or a secret reference. Empty means no check.",
	"admin.tls.certFile":   "Certificate file. Needs `keyFile`.",
	"admin.tls.keyFile":    "Key file. Needs `certFile`.",
	"admin.tls.clientCA":   "CA file. Clients must send a certificate.",
	"admin.tls.clientAuth": "Only `require_and_verify`. Needs `clientCA`.",

	"duckdb.memoryLimit": "DuckDB `memory_limit`.",
	"duckdb.threads":     "DuckDB `threads`. `0` means the DuckDB default.",
	"duckdb.tempDir":     "DuckDB `temp_directory`.",
	"duckdb.maxOpen":     "Maximum open connections.",
	"duckdb.maxIdle":     "Maximum idle connections.",
	"duckdb.connMaxIdle": "Idle time before a connection closes.",

	"datasources.failFast": "Stop at start when a data source fails.",

	"policies.directory":         "The policy directory.",
	"policies.reload":            "Reload when a file changes.",
	"policies.engine":            "`yaml`, `opa` or `composite`.",
	"policies.composite.members": "`yaml`, `opa`, `rebac`.",
	"policies.opa.moduleDir":     "Directory of the `.rego` files.",
	"policies.opa.query":         "Rego query for the decision.",
	"policies.rebac.cacheTtl":    "Lifetime of a cached check.",
	"policies.rebac.cacheSize":   "Maximum cached checks.",

	"audit.file.path":         "Audit log directory. Empty means `sluice-audit` in the temp directory.",
	"audit.file.rotateDaily":  "New file each UTC day. Always on when `rotateSizeMB` is `0`.",
	"audit.file.rotateSizeMB": "New file after this size.",
	"audit.file.genesis":      "Secret reference: genesis.",
	"audit.syslog.network":    "`udp`, `tcp`, `unix` or `unixgram`.",
	"audit.syslog.address":    "Required. `host:port` or socket path.",
	"audit.syslog.facility":   "`kern`, `user`, `daemon`, `auth`, `syslog`, `local0` to `local7`.",
	"audit.syslog.tag":        "Syslog app name.",
	"audit.s3.endpoint":       "S3 host.",
	"audit.s3.bucket":         "Required.",
	"audit.s3.prefix":         "Object key prefix.",
	"audit.s3.region":         "S3 region.",
	"audit.s3.insecure":       "Use HTTP, not HTTPS.",
	"audit.s3.forcePathStyle": "Path-style URLs.",
	"audit.s3.objectLock":     "`governance` or `compliance`. Needs `retentionDays`.",
	"audit.s3.retentionDays":  "Lock time in days.",
	"audit.s3.credentialsRef": "Secret reference: JSON with `accessKeyId`, `secretAccessKey`.",
	"audit.s3.uploadInterval": "Longest time between uploads.",
	"audit.s3.uploadBytes":    "Batch size that starts an upload.",
	"audit.s3.maxBufferBytes": "Buffer size. More records are dropped.",
	"audit.failClosed":        "Refuse queries when the audit log fails.",
	"audit.sqlSampleBytes":    "SQL bytes in an audit record.",

	"logging.level":         "`debug`, `info`, `warn` or `error`.",
	"logging.format":        "`json` or `text`.",
	"logging.maxErrorBytes": "Cut logged errors at this size. `0` means no cut.",

	"tracing.enabled":     "Send OpenTelemetry traces.",
	"tracing.endpoint":    "OTLP `host:port`. Required when enabled.",
	"tracing.protocol":    "`grpc` or `http`.",
	"tracing.insecure":    "Send without TLS.",
	"tracing.sampleRatio": "Share of traces sent, 0 to 1.",

	"identity.apiKeyPepper": "Secret reference: pepper for API key hashes.",

	"limits.maxRows":             "Row limit when the request sets none.",
	"limits.maxRowsCeiling":      "Highest row limit a request can set.",
	"limits.maxResultBytes":      "Size limit of a result in bytes. Sluice cuts the result before the row that crosses it. `0` means no limit.",
	"limits.maxSqlBytes":         "Largest SQL text.",
	"limits.queryTimeout":        "Timeout when the request sets none.",
	"limits.maxQueryTimeout":     "Longest timeout a request can set.",
	"limits.maxConcurrent":       "Queries running at one time.",
	"limits.disableCrossCatalog": "Reject queries that read several catalogs.",
	"limits.globalRps":           "Requests per second of all clients, REST queries and MCP over HTTP together. `0` means no limit.",
	"limits.globalBurst":         "Burst for `globalRps`.",
	"limits.perIpRps":            "Requests per second per client IP, counted like `globalRps`. `0` means no limit.",
	"limits.perIpBurst":          "Burst for `perIpRps`.",
	"limits.perIpMaxBuckets":     "Maximum client IPs tracked.",
	"limits.defaultSubjectRps":   "Requests per second per subject without `rateLimit`. `0` means no limit.",
	"limits.defaultSubjectBurst": "Burst for `defaultSubjectRps`.",

	"cache.rewrite.enabled": "Cache decisions and rewritten SQL.",
	"cache.rewrite.size":    "Maximum cached entries.",
	"cache.rewrite.ttl":     "Lifetime of an entry.",

	"approval.publicBaseUrl":         "Base URL of the accept and reject links.",
	"approval.webhooks[].url":        "Receiver of approval requests.",
	"approval.webhooks[].headersRef": "Secret reference: JSON object of headers.",
	"approval.webhooks[].timeout":    "Timeout of one delivery. At most 10s.",
	"approval.syncWait":              "Wait for a decision before `ERR_APPROVAL_PENDING`. At most `rest.requestTimeout` minus 2 s.",
	"approval.requestTtl":            "Lifetime of a pending approval.",
	"approval.grantTtl":              "Lifetime of a grant.",
	"approval.maxPending":            "Maximum pending approvals.",
	"approval.sqlSampleBytes":        "SQL bytes in the webhook.",
	"approval.persist":               "Keep approvals over a restart.",
	"approval.stateDir":              "Directory of the approval store.",

	"budget.enabled":       "Turn on budgets.",
	"budget.stateDir":      "Directory of the budget store.",
	"budget.flushInterval": "Time between writes to the store.",
	"budget.failClosed":    "Stop at start when the store cannot open.",
	"budget.retentionDays": "Days of usage kept.",
}

const header = `<!-- SPDX-License-Identifier: CC-BY-4.0 -->
<!-- Generated by scripts/gen-config-docs.go. Do not edit by hand. -->

# Configuration

` + "`sluice serve --config <file>`" + ` reads the server config, a YAML file.

An environment variable overrides a key: ` + "`SLUICE_REST__LISTEN`" + ` sets ` + "`rest.listen`" + `.
`

type row struct {
	key, def string
	block    string // optional block that holds the key
}

func main() {
	out := flag.String("out", "docs/reference/configuration.md", "output path")
	check := flag.Bool("check", false, "fail instead of write when output differs")
	flag.Parse()

	defaults := config.DefaultServerConfig()
	var rows []row
	walk(reflect.TypeOf(defaults), reflect.ValueOf(defaults), "", "", &rows)

	var buf bytes.Buffer
	buf.WriteString(header)
	buf.WriteString(envNote(rows))
	buf.WriteString("\nA key with an empty default is a string. Write a time like its default: `30s`, `5m`.\n\n")
	buf.WriteString("| Key | Default | Meaning |\n|---|---|---|\n")
	seen := map[string]bool{}
	for _, r := range rows {
		m, ok := meaning[r.key]
		if !ok {
			fatal("no meaning for %s: add it to the meaning map, or to skip", r.key)
		}
		seen[r.key] = true
		def := r.def
		if def != "" {
			def = "`" + def + "`"
		}
		fmt.Fprintf(&buf, "| `%s` | %s | %s |\n", r.key, def, m)
	}
	for k := range meaning {
		if !seen[k] {
			fatal("meaning for %s matches no key: remove it", k)
		}
	}
	for k := range blockDefault {
		if !seen[k] {
			fatal("blockDefault for %s matches no key: remove it", k)
		}
	}
	for k, unmet := range skip {
		if unmet {
			fatal("skip entry %s matches no key: remove it", k)
		}
	}

	abs, err := filepath.Abs(*out)
	if err != nil {
		fatal("resolve path: %v", err)
	}

	if *check {
		cur, err := os.ReadFile(abs)
		if err != nil {
			fatal("read existing: %v", err)
		}
		if !bytes.Equal(cur, buf.Bytes()) {
			fmt.Fprintln(os.Stderr, "configuration.md drift: run `make docs-generate`")
			os.Exit(1)
		}
		return
	}

	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		fatal("write %s: %v", abs, err)
	}
	fmt.Printf("wrote %s\n", abs)
}

// walk collects one row per leaf key, in declaration order. A struct
// pointer or a list of structs is an optional block: it is nil by default,
// so its keys are read from the zero value of the struct.
func walk(t reflect.Type, v reflect.Value, prefix, block string, rows *[]row) {
	for i := range t.NumField() {
		f := t.Field(i)
		key := f.Tag.Get("mapstructure")
		if prefix != "" {
			key = prefix + "." + key
		}
		ft, fv := f.Type, v.Field(i)
		switch {
		case ft.Kind() == reflect.Struct:
			walk(ft, fv, key, block, rows)
		case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct && !fv.IsNil():
			walk(ft.Elem(), fv.Elem(), key, block, rows)
		case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
			walk(ft.Elem(), reflect.Zero(ft.Elem()), key, key, rows)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			walk(ft.Elem(), reflect.Zero(ft.Elem()), key+"[]", key, rows)
		default:
			if _, ok := skip[key]; ok {
				skip[key] = false
				continue
			}
			def := formatDefault(fv)
			if d, ok := blockDefault[key]; ok {
				def = d
			}
			*rows = append(*rows, row{key: key, def: def, block: block})
		}
	}
}

// envNote names the keys that no SLUICE_* variable sets: a list of blocks
// has no single value.
func envNote(rows []row) string {
	var lists []string
	for _, r := range rows {
		if strings.Contains(r.key, "[]") {
			lists = appendNew(lists, "`"+r.block+"`")
		}
	}
	return "No variable sets " + strings.Join(lists, ", ") + ".\n" +
		"[Server config](../operations/server-config.md) explains secret references.\n"
}

func appendNew(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// formatDefault renders a default value. An empty string gives an empty cell.
func formatDefault(v reflect.Value) string {
	s := fmt.Sprint(v.Interface())
	if _, ok := v.Interface().(time.Duration); ok && strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s") // 30m0s reads better as 30m
	}
	return s
}

func fatal(f string, args ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
	os.Exit(1)
}
