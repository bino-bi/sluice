// SPDX-License-Identifier: Apache-2.0

package mask

import "fmt"

// constantProvider replaces a column value with a fixed scalar. The value
// is bound as a positional SQL parameter so DuckDB handles escaping.
type constantProvider struct{}

func newConstantProvider() Provider { return &constantProvider{} }

// Type returns "constant".
func (constantProvider) Type() string { return "constant" }

// MaskSQL returns a single positional placeholder and binds Args.Value to
// it. Callers merge the returned param into the overall query param list.
func (constantProvider) MaskSQL(ctx MaskContext) (string, []Param, error) {
	return "$1", []Param{{Name: "constant", Value: ctx.Args.Value}}, nil
}

// MaskArrow is not supported by this provider.
func (constantProvider) MaskArrow(_ MaskArrowContext) error {
	return ErrSQLOnly
}

// ValidateArgs accepts scalar values (string, number, bool, nil). Maps and
// slices are rejected because DuckDB cannot bind them as a scalar column
// replacement.
func (constantProvider) ValidateArgs(a Args) error {
	switch v := a.Value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	default:
		return fmt.Errorf("%w: value must be a scalar, got %T", ErrInvalidArgs, v)
	}
}
