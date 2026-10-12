// SPDX-License-Identifier: AGPL-3.0-or-later

package executor_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/parser"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

func newParseExec(t *testing.T, maxSQLBytes int) *executor.Executor {
	t.Helper()
	e, err := executor.NewParseCheck(context.Background(), maxSQLBytes, nil)
	if err != nil {
		t.Fatalf("NewParseCheck: %v", err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

// inList is a statement of about n bytes with a long IN list.
func inList(n int) string {
	var b strings.Builder
	b.WriteString("SELECT id FROM t WHERE id IN (1000000")
	for i := 1000001; b.Len() < n-16; i++ {
		b.WriteString(", " + strconv.Itoa(i))
	}
	b.WriteString(")")
	return b.String()
}

func TestParseTrees(t *testing.T) {
	e := newParseExec(t, 0)
	ctx := context.Background()

	a, b, err := e.ParseTrees(ctx, "SELECT 1 AS x", "SELECT FROM WHERE")
	if err != nil {
		t.Fatalf("ParseTrees: %v", err)
	}
	if !strings.HasPrefix(a, `{"error":false`) || !strings.Contains(a, `"alias":"x"`) {
		t.Errorf("tree of a SELECT = %s", a)
	}
	if !strings.HasPrefix(b, `{"error":true`) {
		t.Errorf("tree of garbage = %s", b)
	}

	var locked bool
	if err := e.DB().QueryRowContext(ctx, "SELECT value::BOOLEAN FROM duckdb_settings() WHERE name='lock_configuration'").Scan(&locked); err != nil {
		t.Fatalf("probe lock_configuration: %v", err)
	}
	if !locked {
		t.Error("parse instance is not hardened")
	}
}

// The texts are bound as values. Text that would close a string literal
// spliced into the query is parsed as a statement of its own.
func TestParseTreesTextStaysData(t *testing.T) {
	e := newParseExec(t, 0)
	ctx := context.Background()

	cases := []struct {
		name, sql string
		want      []string // substrings of the tree
	}{
		{"literal breakout", `x')::VARCHAR, getenv('HOME`, []string{`{"error":true`}},
		{"breakout after a valid statement", `SELECT 1')::VARCHAR, (SELECT getenv('HOME')) --`, []string{`{"error":true`}},
		{"doubled quote", `SELECT 'it''s' AS q`, []string{`"value":"it's"`}},
		{"backslash", `SELECT 'a\b' AS q`, []string{`"value":"a\\b"`}},
		{"escape string", `SELECT E'\x41\'' AS q`, []string{`"value":"A'"`}},
		{"dollar quote", `SELECT $$it's$$ AS q`, []string{`"value":"it's"`}},
		{"comments", "SELECT 1 /* ' */ AS q -- '\n", []string{`"alias":"q"`}},
		{"multibyte", `SELECT 'äöü日本' AS "列"`, []string{`"value":"äöü日本"`, `"alias":"列"`}},
		{"function not run", `SELECT getenv('HOME') AS h`, []string{`"function_name":"getenv"`}},
		{"not a SELECT", `ATTACH 'x.db' AS x`, []string{`{"error":true`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, err := e.ParseTrees(ctx, tc.sql, tc.sql)
			if err != nil {
				t.Fatalf("ParseTrees: %v", err)
			}
			if a != b {
				t.Errorf("same text, different trees:\n%s\n%s", a, b)
			}
			for _, w := range tc.want {
				if !strings.Contains(a, w) {
					t.Errorf("tree lacks %s: %s", w, a)
				}
			}
		})
	}
}

// DuckDB reads a statement up to its first NUL byte, as execution does:
// the driver hands the query text over as a C string.
func TestParseTreesNUL(t *testing.T) {
	e := newParseExec(t, 0)
	ctx := context.Background()

	cut, plain, err := e.ParseTrees(ctx, "SELECT 1\x00, 2", "SELECT 1")
	if err != nil {
		t.Fatalf("ParseTrees: %v", err)
	}
	if cut != plain {
		t.Errorf("text after NUL was parsed:\n%s\n%s", cut, plain)
	}
	open, _, err := e.ParseTrees(ctx, "SELECT 'a\x00b'", "SELECT 1")
	if err != nil {
		t.Fatalf("ParseTrees: %v", err)
	}
	if !strings.HasPrefix(open, `{"error":true`) {
		t.Errorf("literal cut at NUL = %s", open)
	}
}

func TestParseTreesErrors(t *testing.T) {
	e := newParseExec(t, 0)

	if _, _, err := e.ParseTrees(context.Background(), "SELECT '\xff'", "SELECT 1"); err == nil {
		t.Error("invalid UTF-8 returned a tree")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := e.ParseTrees(ctx, "SELECT 1", "SELECT 1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("canceled ctx: err = %v", err)
	}
}

// Two checks of statements just under the default limits.maxSqlBytes fit
// into the memory limit at the same time.
func TestParseTreesMaxSize(t *testing.T) {
	e := newParseExec(t, 0)
	sql := inList(parser.DefaultMaxSQLBytes)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			_, _, errs[i] = e.ParseTrees(context.Background(), sql, sql)
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Errorf("ParseTrees of %d bytes: %v", len(sql), err)
		}
	}
}

// A statement whose trees do not fit is ErrOutOfMemory. The error carries
// the client code and a message that tells the caller to send a shorter
// one. Any maxSQLBytes under 256 KiB gives the 256 MiB floor, which a 1 MiB
// SELECT 1,1,... does not fit into.
func TestParseTreesOutOfMemory(t *testing.T) {
	e := newParseExec(t, 1<<10)
	sql := "SELECT 1" + strings.Repeat(",1", parser.DefaultMaxSQLBytes/2)
	_, _, err := e.ParseTrees(context.Background(), sql, sql)
	if !errors.Is(err, executor.ErrOutOfMemory) {
		t.Fatalf("err = %v, want ErrOutOfMemory", err)
	}
	if ae := pkgerr.FromError(err); ae.Code != pkgerr.CodePayloadTooLarge || !strings.Contains(ae.Message, "shorter statement") {
		t.Fatalf("client error = %s (%s), want ERR_PAYLOAD_TOO_LARGE with a hint", ae.Code, ae.Message)
	}
}
