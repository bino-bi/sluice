// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"errors"
	"strings"
	"testing"
)

const multiDocYAML = `
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: finance-ro
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["finance"]
        resources:
          tables: ["pg.public.orders"]
---
apiVersion: sluice.bino.bi/v1alpha1
kind: DataSource
metadata:
  name: pg-primary
spec:
  type: postgres
  connection: "host=localhost"
  readonly: true
---
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: mask-email
spec:
  match:
    any:
      - resources:
          columns: ["pg.public.users.email"]
  mask:
    type: partial
    args:
      showFirst: 2
      showLast: 0
      maskChar: "*"
`

func TestDecodeMultiDoc(t *testing.T) {
	t.Parallel()
	objs, err := Decode(strings.NewReader(multiDocYAML))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(objs) != 3 {
		t.Fatalf("got %d docs, want 3", len(objs))
	}
	kinds := []Kind{}
	for _, o := range objs {
		kinds = append(kinds, o.GetKind())
	}
	want := []Kind{KindSQLAccessPolicy, KindDataSource, KindColumnMaskPolicy}
	for i, k := range want {
		if kinds[i] != k {
			t.Errorf("doc %d: kind = %q, want %q", i, kinds[i], k)
		}
	}

	access, ok := objs[0].(*SQLAccessPolicy)
	if !ok {
		t.Fatalf("doc 0 is %T, want *SQLAccessPolicy", objs[0])
	}
	if access.Metadata.Name != "finance-ro" {
		t.Errorf("name = %q, want finance-ro", access.Metadata.Name)
	}
	if access.Spec.Effect != EffectAllow {
		t.Errorf("effect = %q, want allow", access.Spec.Effect)
	}
}

func TestDecodeRejectsMissingAPIVersion(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`
kind: SqlAccessPolicy
metadata:
  name: x
`))
	if err == nil {
		t.Fatal("expected error for missing apiVersion")
	}
}

func TestDecodeRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: NotARealKind
metadata:
  name: x
`))
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestDecodeValidatesMissingName(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata: {}
spec:
  effect: allow
  match:
    any:
      - subjects: {groups: ["x"]}
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "metadata.name" {
		t.Errorf("Field = %q, want metadata.name", ve.Field)
	}
}

// A manifest of kind AuditSink never reached the audit pipeline: the sinks
// come from audit.* in the server config. It is refused, also in strict
// mode, with a message that names the place that works.
func TestDecodeRejectsAuditSinkKind(t *testing.T) {
	t.Parallel()
	const doc = `
apiVersion: sluice.bino.bi/v1alpha1
kind: AuditSink
metadata:
  name: local
spec:
  type: file
  path: /var/lib/sluice/audit
`
	for _, strict := range []bool{false, true} {
		_, err := Decode(strings.NewReader(doc), WithStrictUnknown(strict))
		if err == nil || !strings.Contains(err.Error(), "audit.file") {
			t.Errorf("strict=%v: err = %v, want a pointer to audit.* in the server config", strict, err)
		}
	}
}

// A field that Sluice never acted on is refused when it is set, so that an
// operator does not rely on it.
func TestDecodeRejectsFieldsWithoutEffect(t *testing.T) {
	t.Parallel()
	ds := func(spec string) string {
		return "apiVersion: sluice.bino.bi/v1alpha1\nkind: DataSource\nmetadata:\n  name: t\nspec:\n  type: sqlite\n  path: /data/shop.db\n" + spec
	}
	binding := "apiVersion: sluice.bino.bi/v1alpha1\nkind: SubjectBinding\nmetadata:\n  name: b\nspec:\n  issuer: https://idp.test\n"
	rebac := func(backend string) string {
		return "apiVersion: sluice.bino.bi/v1beta1\nkind: RelationshipPolicy\nmetadata:\n  name: r\nspec:\n" +
			"  match:\n    any:\n      - resources:\n          tables: [\"docs\"]\n" +
			"  backend:\n    endpoint: http://fga:8080\n    storeId: s1\n" + backend +
			"  checks:\n    - objectTemplate: \"doc:{{table}}\"\n      relation: viewer\n"
	}
	cases := []struct {
		name      string
		yaml      string
		wantField string // empty = decode must succeed
	}{
		{"plain data source", ds(""), ""},
		{"readonly true", ds("  readonly: true\n"), ""},
		{"readonly false", ds("  readonly: false\n"), "spec.readonly"},
		{"attachMode readonly", ds("  attachMode: readonly\n"), ""},
		{"attachMode readwrite", ds("  attachMode: readwrite\n"), "spec.attachMode"},
		{"schemas", ds("  schemas: [main]\n"), "spec.schemas"},
		{"tables", ds("  tables: [orders]\n"), "spec.tables"},
		{"healthCheck", ds("  healthCheck:\n    query: SELECT 1\n    interval: 10s\n"), "spec.healthCheck"},
		{"prefix", "apiVersion: sluice.bino.bi/v1alpha1\nkind: DataSource\nmetadata:\n  name: t\nspec:\n  type: s3\n  bucket: b\n  prefix: sales/\n", "spec.prefix"},
		{"binding without groupsSource", binding, ""},
		{"groupsSource", binding + "  groupsSource: claims\n", "spec.groupsSource"},
		{"backend without type", rebac(""), ""},
		{"backend type openfga", rebac("    type: openfga\n"), ""},
		{"backend type spicedb", rebac("    type: spicedb\n"), "spec.backend.type"},
		{"backend cacheTtl", rebac("    cacheTtl: 30s\n"), "spec.backend.cacheTtl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode(strings.NewReader(tc.yaml))
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("Decode: %v, want no error", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.wantField {
				t.Fatalf("err = %v, want a ValidationError on %s", err, tc.wantField)
			}
		})
	}
}

func TestDecodeValidatesFilterExclusivity(t *testing.T) {
	t.Parallel()
	// Both predicate and expression set → invalid.
	_, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: RowFilterPolicy
metadata:
  name: bad-filter
spec:
  match:
    any:
      - resources: {tables: ["pg.public.orders"]}
  filter:
    predicate:
      column: tenant_id
      op: Equals
      value: "1"
    expression: "tenant_id == '1'"
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(ve.Reason, "exactly one") {
		t.Errorf("Reason = %q, want exactly-one hint", ve.Reason)
	}
}

func TestDecodeColumnMaskPartialRequiresShow(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: bad-partial
spec:
  match:
    any:
      - resources: {columns: ["pg.public.users.email"]}
  mask:
    type: partial
    args:
      maskChar: "*"
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(ve.Reason, "partial mask") {
		t.Errorf("Reason = %q, want partial-mask hint", ve.Reason)
	}
}

func TestDecodeMaskExtrasPreserved(t *testing.T) {
	t.Parallel()
	// Unknown mask arg should land in Extras (non-strict mode).
	objs, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: ColumnMaskPolicy
metadata:
  name: future-mask
spec:
  match:
    any:
      - resources: {columns: ["pg.public.users.email"]}
  mask:
    type: partial
    args:
      showFirst: 1
      futureField: 42
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	cm, ok := objs[0].(*ColumnMaskPolicy)
	if !ok {
		t.Fatalf("got %T, want *ColumnMaskPolicy", objs[0])
	}
	if cm.Spec.Mask.Args.Extras == nil {
		t.Fatal("Extras should capture unknown fields, got nil")
	}
	got, ok := cm.Spec.Mask.Args.Extras["futureField"]
	if !ok {
		t.Fatalf("Extras missing futureField: %v", cm.Spec.Mask.Args.Extras)
	}
	// YAML numbers may decode as json.Number, float64, or int depending on
	// the path. Any of those is acceptable as long as the value survives.
	switch v := got.(type) {
	case float64:
		if v != 42 {
			t.Errorf("futureField = %v, want 42", v)
		}
	case int:
		if v != 42 {
			t.Errorf("futureField = %v, want 42", v)
		}
	case int64:
		if v != 42 {
			t.Errorf("futureField = %v, want 42", v)
		}
	default:
		t.Errorf("futureField type = %T value = %v, expected a number", got, got)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	t.Parallel()
	e := &ValidationError{
		Kind: KindSQLAccessPolicy, Name: "finance-ro",
		Field: "spec.effect", Reason: `must be "allow" or "deny"`,
	}
	got := e.Error()
	for _, want := range []string{"SqlAccessPolicy", "finance-ro", "spec.effect"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}
}

func TestValidateDataSourceS3AndHTTP(t *testing.T) {
	t.Parallel()
	mk := func(spec string) string {
		return "apiVersion: sluice.bino.bi/v1alpha1\nkind: DataSource\nmetadata:\n  name: t\nspec:\n" + spec
	}
	cases := []struct {
		name      string
		yaml      string
		wantField string // empty = decode must succeed
	}{
		{"s3 ok", mk("  type: s3\n  bucket: b\n  region: eu-central-1\n"), ""},
		{"s3 with format ok", mk("  type: s3\n  bucket: b\n  format: csv\n"), ""},
		{"s3 missing bucket", mk("  type: s3\n  region: eu-central-1\n"), "spec.bucket"},
		{"s3 bad format", mk("  type: s3\n  bucket: b\n  format: avro\n"), "spec.format"},
		{"http ok", mk("  type: http\n  urls: [\"https://example.com/data.csv\"]\n"), ""},
		{"http with headersRef ok", mk("  type: http\n  urls: [\"https://example.com/d.json\"]\n  headersRef: secret://env/API_HEADERS\n"), ""},
		{"http missing urls", mk("  type: http\n"), "spec.urls"},
		{"http bad scheme", mk("  type: http\n  urls: [\"ftp://example.com/x.csv\"]\n"), "spec.urls[0]"},
		{"http relative url", mk("  type: http\n  urls: [\"/local/x.csv\"]\n"), "spec.urls[0]"},
		{"s3_parquet renamed", mk("  type: s3_parquet\n  bucket: b\n"), "spec.type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tc.yaml))
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("Decode: %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v; want ValidationError", err)
			}
			if ve.Field != tc.wantField {
				t.Fatalf("Field = %q; want %q", ve.Field, tc.wantField)
			}
		})
	}
}
