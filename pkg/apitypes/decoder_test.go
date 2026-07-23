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

func TestDecodeRejectsUnimplementedAuditSink(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: AuditSink
metadata:
  name: siem
spec:
  type: postgres
  connection: postgres://audit
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "spec.type" {
		t.Errorf("Field = %q, want spec.type", ve.Field)
	}
	if !strings.Contains(ve.Reason, "parsed but unimplemented") {
		t.Errorf("Reason = %q, want parsed-but-unimplemented", ve.Reason)
	}
}

func TestDecodeRejectsServerConfigAuditSinkManifest(t *testing.T) {
	t.Parallel()
	_, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: AuditSink
metadata:
  name: siem
spec:
  type: s3
  bucket: audit-bucket
`))
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Field != "spec.type" {
		t.Errorf("Field = %q, want spec.type", ve.Field)
	}
	if !strings.Contains(ve.Reason, "audit.s3 in sluice.yaml") {
		t.Errorf("Reason = %q, want pointer to server config", ve.Reason)
	}
}

func TestDecodeAcceptsFileAuditSink(t *testing.T) {
	t.Parallel()
	objs, err := Decode(strings.NewReader(`
apiVersion: sluice.bino.bi/v1alpha1
kind: AuditSink
metadata:
  name: local
spec:
  type: file
  path: /var/lib/sluice/audit
`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("objects = %d, want 1", len(objs))
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
