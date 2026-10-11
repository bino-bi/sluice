// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/mask"
)

// snippetMask is a provider that returns a fixed snippet.
type snippetMask struct {
	snippet string
	params  []mask.Param
}

func (snippetMask) Type() string { return "snippet" }

func (p snippetMask) MaskSQL(mask.MaskContext) (string, []mask.Param, error) {
	return p.snippet, p.params, nil
}

func (snippetMask) MaskArrow(mask.MaskArrowContext) error { return mask.ErrSQLOnly }

func (snippetMask) ValidateArgs(mask.Args) error { return nil }

func snippetRewriter(t *testing.T, fc *fakeChecker, p snippetMask) *rewriter.Rewriter {
	t.Helper()
	reg := mask.NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	return rewriter.New(rewriter.Options{
		Parser:         pgquery.New(parser.Options{}),
		DefaultCatalog: "pg",
		Masks:          reg,
		ParseCheck:     fc,
		Schema:         staticSchema(map[string][]string{"pg.public.orders": {"id", "secret"}}),
	})
}

// rewriteSecret rewrites sql with the snippet mask on orders.secret.
func rewriteSecret(ctx context.Context, t *testing.T, rw *rewriter.Rewriter, sql string) (*rewriter.RewriteResult, error) {
	t.Helper()
	ast, err := pgquery.New(parser.Options{}).Parse(context.Background(), sql)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dec := &policy.Decision{
		Outcome: policy.OutcomeAllow,
		ColumnMasks: map[string]*policy.CompiledMask{
			"pg.public.orders.secret": {TableKey: "pg.public.orders", Column: "secret", Type: "snippet", Policy: "mask-secret"},
		},
	}
	return rw.Rewrite(ctx, rewriter.RewriteRequest{AST: ast, Decision: dec, Raw: sql})
}

// duckChecker passes every check to the DuckDB parse instance.
func duckChecker(t *testing.T) *fakeChecker {
	t.Helper()
	exec, err := goldenParseCheck()
	if err != nil {
		t.Fatalf("parse check executor: %v", err)
	}
	return &fakeChecker{fn: exec.ParseTrees}
}

// snippetChecks counts the checks of snippet.
func snippetChecks(fc *fakeChecker, snippet string) int {
	n := 0
	for _, c := range fc.calls {
		if c[0] == "SELECT "+snippet {
			n++
		}
	}
	return n
}

const secretSQL = "SELECT o.secret FROM pg.public.orders o"

// pg_query deparses INTERVAL '14' DAY as 14 seconds in DuckDB. The snippet
// is normalized, and the check proves DuckDB reads it as written.
func TestMaskSnippet_Normalized(t *testing.T) {
	fc := duckChecker(t)
	snippet := "CASE WHEN __col__ IS NULL THEN NULL ELSE __col__ + INTERVAL '14' DAY END"
	res, err := rewriteSecret(context.Background(), t, snippetRewriter(t, fc, snippetMask{snippet: snippet}), secretSQL)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if !strings.Contains(res.SQL, "sluice_src.secret + to_days(trunc('14'::double precision)::int)") {
		t.Errorf("SQL = %s, want the to_days expansion", res.SQL)
	}
	if n := snippetChecks(fc, snippet); n != 1 {
		t.Errorf("snippet checked %d times, want 1", n)
	}
}

// The snippet is a whole select item of the table wrapper, the position
// the DuckDB check sees it in. So any top node keeps its meaning, also an
// operator or a function in SQL syntax, and the query's expression reads
// the wrapper column.
func TestMaskSnippet_WholeWrapperItem(t *testing.T) {
	for snippet, want := range map[string]string{
		"__col__ || 'x'":                            "sluice_src.secret || 'x' AS secret",
		"__col__ IS NULL":                           "sluice_src.secret IS NULL AS secret",
		"__col__ AT TIME ZONE 'UTC'":                "sluice_src.secret AT TIME ZONE 'UTC' AS secret",
		"greatest(__col__, 0)":                      "GREATEST(sluice_src.secret, 0) AS secret",
		"least(__col__, 0)":                         "LEAST(sluice_src.secret, 0) AS secret",
		"nullif(__col__, '')":                       "NULLIF(sluice_src.secret, '') AS secret",
		"trim(__col__)":                             "TRIM (BOTH  FROM sluice_src.secret) AS secret",
		"TRIM(LEADING 'a' FROM __col__)":            "TRIM (LEADING 'a' FROM sluice_src.secret) AS secret",
		"EXTRACT(year FROM __col__)":                "extract ('year' FROM sluice_src.secret) AS secret",
		"substring(__col__ FROM 1 FOR 3)":           "SUBSTRING(sluice_src.secret FROM 1 FOR 3) AS secret",
		"position('a' IN __col__)":                  "POSITION('a' IN sluice_src.secret) AS secret",
		"overlay(__col__ PLACING '*' FROM 1 FOR 2)": "OVERLAY(sluice_src.secret PLACING '*' FROM 1 FOR 2) AS secret",
		"normalize(__col__)":                        "normalize (sluice_src.secret) AS secret",
	} {
		t.Run(snippet, func(t *testing.T) {
			res, err := rewriteSecret(context.Background(), t, snippetRewriter(t, duckChecker(t), snippetMask{snippet: snippet}),
				"SELECT o.secret || 'x' FROM pg.public.orders o")
			if err != nil {
				t.Fatalf("rewrite: %v", err)
			}
			if !strings.Contains(res.SQL, want) || !strings.HasPrefix(res.SQL, "SELECT o.secret || 'x' FROM (") {
				t.Errorf("SQL = %s, want %s in the wrapper", res.SQL, want)
			}
		})
	}
}

func TestMaskSnippet_CheckRefuses(t *testing.T) {
	// The deparse drops the parentheses, and DuckDB reads
	// __col__ = 1 OR (__col__ = 2 IS NULL).
	snippet := "CASE WHEN (__col__ = 1 OR __col__ = 2) IS NULL THEN 'a' ELSE 'b' END"
	res, err := rewriteSecret(context.Background(), t, snippetRewriter(t, duckChecker(t), snippetMask{snippet: snippet}), secretSQL)
	if !errors.Is(err, rewriter.ErrMaskUnsupported) || !strings.Contains(err.Error(), "mask snippet changes meaning in DuckDB") {
		t.Fatalf("err = %v, SQL = %v, want the snippet refusal", err, res)
	}
}

// Every SQL snippet of a built-in provider passes the DuckDB check.
func TestMaskSnippet_BuiltinsPass(t *testing.T) {
	args := map[string][]mask.Args{
		"null":     {{}},
		"constant": {{Value: "x"}},
		"partial":  {{ShowFirst: 1, ShowLast: 2}},
		"hash":     {{}, {SaltRef: "secret://env/SALT"}, {Algorithm: "hmac_sha256", SaltRef: "secret://env/SALT"}},
		"regex":    {{Pattern: "a", Replacement: "b"}},
		"truncate": {{Length: 3, Suffix: "..."}},
		"fpe":      {{}},
		"fake":     {{}},
		"jitter":   {{}},
	}
	reg := mask.Default()
	for _, typ := range reg.Types() {
		cases, ok := args[typ]
		if !ok {
			t.Errorf("built-in mask %q has no case here", typ)
			continue
		}
		p, _ := reg.Lookup(typ)
		for _, a := range cases {
			if mask.IsPostQuery(p, a) {
				continue
			}
			snippet, params, err := p.MaskSQL(mask.MaskContext{Ctx: context.Background(), Args: a, SaltStore: goldenSaltStore{}})
			if err != nil {
				t.Errorf("%s %+v: MaskSQL: %v", typ, a, err)
				continue
			}
			fc := duckChecker(t)
			if _, err := rewriteSecret(context.Background(), t, snippetRewriter(t, fc, snippetMask{snippet: snippet, params: params}), secretSQL); err != nil {
				t.Errorf("%s snippet %q: %v", typ, snippet, err)
			}
			if n := snippetChecks(fc, snippet); n != 1 {
				t.Errorf("%s snippet %q checked %d times, want 1", typ, snippet, n)
			}
		}
	}
}

// The verdict for a snippet text is cached across requests. A failed check
// is not.
func TestMaskSnippet_CheckMemo(t *testing.T) {
	const snippet = "upper(__col__)"
	const twice = "SELECT o.secret, lower(o.secret) FROM pg.public.orders o"
	ctx := context.Background()

	t.Run("served", func(t *testing.T) {
		fc := &fakeChecker{fn: trees(`{"error":false}`, `{"error":false}`)}
		rw := snippetRewriter(t, fc, snippetMask{snippet: snippet})
		for range 3 {
			if _, err := rewriteSecret(ctx, t, rw, twice); err != nil {
				t.Fatalf("rewrite: %v", err)
			}
		}
		if n := snippetChecks(fc, snippet); n != 1 {
			t.Errorf("snippet checked %d times, want 1", n)
		}
	})

	t.Run("refused", func(t *testing.T) {
		fc := &fakeChecker{fn: func(_ context.Context, a, _ string) (string, string, error) {
			if a == "SELECT "+snippet {
				return `{"error":false,"a":1}`, `{"error":false,"a":2}`, nil
			}
			return `{"error":false}`, `{"error":false}`, nil
		}}
		rw := snippetRewriter(t, fc, snippetMask{snippet: snippet})
		for range 2 {
			if _, err := rewriteSecret(ctx, t, rw, secretSQL); !errors.Is(err, rewriter.ErrMaskUnsupported) {
				t.Fatalf("err = %v, want ErrMaskUnsupported", err)
			}
		}
		if n := snippetChecks(fc, snippet); n != 1 {
			t.Errorf("snippet checked %d times, want 1", n)
		}
	})

	t.Run("checker failure", func(t *testing.T) {
		boom := errors.New("parse instance gone")
		fail := true
		fc := &fakeChecker{fn: func(_ context.Context, a, _ string) (string, string, error) {
			if a == "SELECT "+snippet && fail {
				fail = false
				return "", "", boom
			}
			return `{"error":false}`, `{"error":false}`, nil
		}}
		rw := snippetRewriter(t, fc, snippetMask{snippet: snippet})
		_, err := rewriteSecret(ctx, t, rw, secretSQL)
		if !errors.Is(err, boom) || errors.Is(err, rewriter.ErrMaskUnsupported) {
			t.Fatalf("err = %v, want the checker error alone", err)
		}
		if _, err := rewriteSecret(ctx, t, rw, secretSQL); err != nil {
			t.Fatalf("second rewrite: %v", err)
		}
		if n := snippetChecks(fc, snippet); n != 2 {
			t.Errorf("snippet checked %d times, want 2", n)
		}
	})

	t.Run("canceled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		fail := true
		fc := &fakeChecker{fn: func(_ context.Context, a, _ string) (string, string, error) {
			if a == "SELECT "+snippet && fail {
				fail = false
				cancel()
				return "", "", errors.New("interrupted")
			}
			return `{"error":false}`, `{"error":false}`, nil
		}}
		rw := snippetRewriter(t, fc, snippetMask{snippet: snippet})
		if _, err := rewriteSecret(cctx, t, rw, secretSQL); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if _, err := rewriteSecret(ctx, t, rw, secretSQL); err != nil {
			t.Fatalf("rewrite after the cancel: %v", err)
		}
		if n := snippetChecks(fc, snippet); n != 2 {
			t.Errorf("snippet checked %d times, want 2", n)
		}
	})
}
