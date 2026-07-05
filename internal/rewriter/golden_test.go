// SPDX-License-Identifier: AGPL-3.0-or-later

package rewriter_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/identity"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/pgquery"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/internal/rewriter"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// updateGolden regenerates every expected.json / expected.sql file. Run
// via `go test ./internal/rewriter -run TestGoldenRewrites -update`.
// Every diff must be reviewed in the PR.
var updateGolden = flag.Bool("update", false, "overwrite golden files with observed output")

// goldenIdentity mirrors the subset of identity.UserCtx carried in
// identity.yaml fixtures. Only the fields used by the selector/template
// engines are captured; middleware-populated metadata (RequestID,
// RemoteAddr, AuthTime) is elided to keep fixtures stable.
type goldenIdentity struct {
	Subject string         `json:"subject" yaml:"subject"`
	Issuer  string         `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	Email   string         `json:"email,omitempty" yaml:"email,omitempty"`
	Groups  []string       `json:"groups,omitempty" yaml:"groups,omitempty"`
	Claims  map[string]any `json:"claims,omitempty" yaml:"claims,omitempty"`
}

// goldenExpected is the on-disk expected.json shape.
type goldenExpected struct {
	Outcome     string    `json:"outcome"` // allow|deny|reject
	SQL         string    `json:"sql,omitempty"`
	Changed     bool      `json:"changed"`
	Params      []any     `json:"params,omitempty"`
	DenyPolicy  string    `json:"deny_policy,omitempty"`
	DenyCode    string    `json:"deny_code,omitempty"`
	Rejections  []string  `json:"rejections,omitempty"` // "policy/rule"
	ErrorCode   string    `json:"error_code,omitempty"` // pkg/errors Code when rewriter returns an APIError
	AppliedKeys []string  `json:"applied_policies,omitempty"`
	Fingerprint string    `json:"fingerprint,omitempty"` // kept for audit wiring; may be empty
	Masks       []string  `json:"masks,omitempty"`       // "table.column=maskType" for quick-grep
	Filters     []string  `json:"filters,omitempty"`     // sorted table keys with active filter
	_           [0]string // guard against accidental field reorder — JSON keys are the ABI
}

func TestGoldenRewrites(t *testing.T) {
	dirs, err := filepath.Glob("testdata/rewrites/*")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(dirs) == 0 {
		t.Skip("no golden fixtures — populate testdata/rewrites/ first")
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			runGoldenFixture(t, dir)
		})
	}
}

func runGoldenFixture(t *testing.T, dir string) {
	t.Helper()

	sql := mustReadFile(t, filepath.Join(dir, "input.sql"))
	sql = strings.TrimRight(sql, "\n")

	policiesYAML := mustReadFile(t, filepath.Join(dir, "policies.yaml"))
	var objs []apitypes.Object
	if strings.TrimSpace(stripYAMLComments(policiesYAML)) != "" {
		decoded, err := apitypes.Decode(strings.NewReader(policiesYAML))
		if err != nil {
			t.Fatalf("decode policies: %v", err)
		}
		objs = decoded
	}

	var idFixture goldenIdentity
	if data, err := os.ReadFile(filepath.Join(dir, "identity.yaml")); err == nil {
		if err := yaml.Unmarshal(data, &idFixture); err != nil {
			t.Fatalf("decode identity: %v", err)
		}
	}
	user := &identity.UserCtx{
		Subject: idFixture.Subject,
		Issuer:  idFixture.Issuer,
		Email:   idFixture.Email,
		Groups:  idFixture.Groups,
		Claims:  idFixture.Claims,
	}
	if user.Subject == "" {
		user = nil
	}

	// Parse + evaluate policy — on parse failure let the rewriter see a
	// nil AST so regex fallback + statement-kind gates exercise.
	p := pgquery.New(parser.Options{})
	ast, parseErr := p.Parse(context.Background(), sql)

	eng := policy.New(policy.Options{})
	src := &config.Snapshot{
		Policies: append([]apitypes.Object(nil), objs...),
		ByKind:   map[apitypes.Kind][]apitypes.Object{},
	}
	for _, o := range objs {
		src.ByKind[o.GetKind()] = append(src.ByKind[o.GetKind()], o)
	}
	if err := eng.ApplySnapshot(context.Background(), src); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	evalIn := policy.Input{User: user}
	if ast != nil {
		evalIn.AST = ast
		evalIn.Shape = ast.Shape()
		evalIn.Tables = ast.Tables()
	}
	dec, err := eng.Evaluate(context.Background(), evalIn)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	rw := rewriter.New(rewriter.Options{
		Parser:         p,
		DefaultCatalog: "pg",
		Salts:          goldenSaltStore{},
	})

	req := rewriter.RewriteRequest{
		AST:      ast,
		Decision: dec,
		User:     user,
		Raw:      sql,
	}
	res, rewriteErr := rw.Rewrite(context.Background(), req)

	got := goldenExpected{Outcome: string(dec.Outcome)}
	switch {
	case rewriteErr != nil:
		var apiErr *pkgerr.APIError
		if errors.As(rewriteErr, &apiErr) {
			got.ErrorCode = string(apiErr.Code)
			got.DenyPolicy = apiErr.Policy
		}
	case res != nil:
		got.SQL = res.SQL
		got.Changed = res.Changed
		got.Params = res.Params
		got.Fingerprint = res.Fingerprint
	}
	if parseErr != nil && rewriteErr != nil && got.ErrorCode == "" {
		got.ErrorCode = "ERR_SYNTAX"
	}
	if dec.DenyReason != nil {
		got.DenyPolicy = dec.DenyReason.PolicyName
		got.DenyCode = dec.DenyReason.Code
	}
	for _, r := range dec.Rejections {
		got.Rejections = append(got.Rejections, r.PolicyName+"/"+r.RuleName)
	}
	for _, a := range dec.Applied {
		got.AppliedKeys = append(got.AppliedKeys, string(a.Kind)+"/"+a.Name)
	}
	for k, m := range dec.ColumnMasks {
		got.Masks = append(got.Masks, k+"="+string(m.Type))
	}
	for k := range dec.RowFilters {
		got.Filters = append(got.Filters, k)
	}
	sort.Strings(got.Masks)
	sort.Strings(got.Filters)
	sort.Strings(got.Rejections)
	sort.Strings(got.AppliedKeys)

	expectedPath := filepath.Join(dir, "expected.json")
	if *updateGolden {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(expectedPath, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %s", expectedPath)
		return
	}

	wantData, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read expected.json: %v (run with -update to bootstrap)", err)
	}
	var want goldenExpected
	if err := json.Unmarshal(wantData, &want); err != nil {
		t.Fatalf("unmarshal expected.json: %v", err)
	}

	compareExpected(t, want, got)
}

func compareExpected(t *testing.T, want, got goldenExpected) {
	t.Helper()
	if want.Outcome != got.Outcome {
		t.Errorf("outcome: want %q got %q", want.Outcome, got.Outcome)
	}
	if normaliseSQL(want.SQL) != normaliseSQL(got.SQL) {
		t.Errorf("sql mismatch:\n want: %s\n got:  %s", want.SQL, got.SQL)
	}
	if want.Changed != got.Changed {
		t.Errorf("changed: want %v got %v", want.Changed, got.Changed)
	}
	if !paramsEqual(want.Params, got.Params) {
		t.Errorf("params:\n want: %#v\n got:  %#v", want.Params, got.Params)
	}
	if want.DenyPolicy != got.DenyPolicy {
		t.Errorf("deny_policy: want %q got %q", want.DenyPolicy, got.DenyPolicy)
	}
	if want.ErrorCode != got.ErrorCode {
		t.Errorf("error_code: want %q got %q", want.ErrorCode, got.ErrorCode)
	}
	if !stringSliceEqual(want.Rejections, got.Rejections) {
		t.Errorf("rejections: want %v got %v", want.Rejections, got.Rejections)
	}
	if !stringSliceEqual(want.Masks, got.Masks) {
		t.Errorf("masks: want %v got %v", want.Masks, got.Masks)
	}
	if !stringSliceEqual(want.Filters, got.Filters) {
		t.Errorf("filters: want %v got %v", want.Filters, got.Filters)
	}
	// Fingerprint intentionally not asserted — pg_query bumps the hash
	// every release and noise shouldn't block SQL-level goldens.
}

func normaliseSQL(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func paramsEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		// YAML decodes numbers as float64 through json; stringify both
		// sides for a permissive compare that still catches typos.
		if toStr(a[i]) != toStr(b[i]) {
			return false
		}
	}
	return true
}

func toStr(v any) string {
	if v == nil {
		return "<nil>"
	}
	return strings.TrimSpace(string(mustJSON(v)))
}

func mustJSON(v any) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		return []byte("<err>")
	}
	return out
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stripYAMLComments removes `#` comments and blank lines so a
// comment-only fixture counts as "no policies" for default-deny tests.
func stripYAMLComments(s string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(s, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// goldenSaltStore resolves every salt reference to a fixed value so
// salted-mask fixtures stay deterministic.
type goldenSaltStore struct{}

func (goldenSaltStore) Get(_ context.Context, _ string) ([]byte, error) {
	return []byte("golden-salt"), nil
}
