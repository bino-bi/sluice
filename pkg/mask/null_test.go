// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"errors"
	"testing"
)

func TestNullProvider(t *testing.T) {
	t.Parallel()
	p := newNullProvider()
	if p.Type() != "null" {
		t.Errorf("Type() = %q, want null", p.Type())
	}
	sql, params, err := p.MaskSQL(MaskContext{})
	if err != nil {
		t.Fatalf("MaskSQL: %v", err)
	}
	if sql != "NULL" {
		t.Errorf("MaskSQL sql = %q, want NULL", sql)
	}
	if len(params) != 0 {
		t.Errorf("MaskSQL params = %v, want none", params)
	}

	if err := p.MaskArrow(MaskArrowContext{}); !errors.Is(err, ErrSQLOnly) {
		t.Errorf("MaskArrow err = %v, want ErrSQLOnly", err)
	}
	if err := p.ValidateArgs(Args{}); err != nil {
		t.Errorf("ValidateArgs: %v", err)
	}
}
