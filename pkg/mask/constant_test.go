// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"errors"
	"testing"
)

func TestConstantProviderMaskSQL(t *testing.T) {
	t.Parallel()
	p := newConstantProvider()
	if p.Type() != "constant" {
		t.Errorf("Type() = %q, want constant", p.Type())
	}
	sql, params, err := p.MaskSQL(MaskContext{Args: Args{Value: "[REDACTED]"}})
	if err != nil {
		t.Fatalf("MaskSQL: %v", err)
	}
	if sql != "?" {
		t.Errorf("MaskSQL sql = %q, want ?", sql)
	}
	if len(params) != 1 || params[0].Value != "[REDACTED]" {
		t.Errorf("MaskSQL params = %v, want [{constant [REDACTED]}]", params)
	}
}

func TestConstantProviderValidateArgs(t *testing.T) {
	t.Parallel()
	p := newConstantProvider()
	good := []any{nil, true, "x", 1, int64(1), 1.5, float32(1), uint64(1)}
	for _, v := range good {
		if err := p.ValidateArgs(Args{Value: v}); err != nil {
			t.Errorf("ValidateArgs(%T %v): %v", v, v, err)
		}
	}
	bad := []any{
		map[string]string{"k": "v"},
		[]int{1, 2, 3},
		struct{ X int }{1},
	}
	for _, v := range bad {
		err := p.ValidateArgs(Args{Value: v})
		if !errors.Is(err, ErrInvalidArgs) {
			t.Errorf("ValidateArgs(%T %v): err = %v, want ErrInvalidArgs", v, v, err)
		}
	}
}

func TestConstantProviderMaskArrowReturnsSQLOnly(t *testing.T) {
	t.Parallel()
	p := newConstantProvider()
	if err := p.MaskArrow(MaskArrowContext{}); !errors.Is(err, ErrSQLOnly) {
		t.Errorf("MaskArrow = %v, want ErrSQLOnly", err)
	}
}
