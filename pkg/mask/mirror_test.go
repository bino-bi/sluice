// SPDX-License-Identifier: Apache-2.0

package mask_test

import (
	"reflect"
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
	"github.com/bino-bi/sluice/pkg/mask"
)

// TestMaskArgsMirrorsAPITypes is the load-bearing invariant test for this
// package. It walks the fields of apitypes.MaskArgs and mask.Args, and
// fails if the two drift: any mask arg added to one side must be mirrored
// on the other. mask.Args is permitted to carry two extra fields the DSL
// does not carry at the MaskArgs level:
//
//   - Expression: synthesized from apitypes.MaskSpec.Expression (a sibling
//     of MaskSpec.Args) by the rewriter.
//   - Extras: forward-compat passthrough that preserves unknown fields
//     from the YAML source.
//
// The comparison is by field NAME only. Types can differ intentionally
// (apitypes.MaskArgs.Algorithm is apitypes.HashAlgo; mask.Args.Algorithm
// is plain string so the layering stays one-way).
func TestMaskArgsMirrorsAPITypes(t *testing.T) {
	t.Parallel()

	apiFields := fieldNames(reflect.TypeFor[apitypes.MaskArgs]())
	maskFields := fieldNames(reflect.TypeFor[mask.Args]())

	// Fields that mask.Args carries in addition to apitypes.MaskArgs.
	extraAllowed := map[string]bool{
		"Expression": true,
	}

	// Present in apitypes but missing from mask.Args → drift.
	for name := range apiFields {
		if _, ok := maskFields[name]; !ok {
			t.Errorf("mirror drift: apitypes.MaskArgs has field %q but mask.Args does not", name)
		}
	}

	// Present in mask.Args but missing from apitypes.MaskArgs (and not in
	// the extras allowlist) → drift.
	for name := range maskFields {
		if _, ok := apiFields[name]; ok {
			continue
		}
		if extraAllowed[name] {
			continue
		}
		t.Errorf("mirror drift: mask.Args has field %q but apitypes.MaskArgs does not (add to extras allowlist if intentional)", name)
	}

	// Sanity check: both structs must have non-zero field counts.
	if len(apiFields) == 0 {
		t.Error("apitypes.MaskArgs has zero exported fields — reflection target missing?")
	}
	if len(maskFields) == 0 {
		t.Error("mask.Args has zero exported fields — reflection target missing?")
	}
}

// fieldNames returns the exported field names of a struct type.
func fieldNames(t reflect.Type) map[string]struct{} {
	if t.Kind() != reflect.Struct {
		panic("fieldNames: not a struct: " + t.String())
	}
	out := make(map[string]struct{}, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		out[f.Name] = struct{}{}
	}
	return out
}
