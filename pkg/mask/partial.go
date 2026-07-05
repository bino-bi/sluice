// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"fmt"
	"unicode/utf8"
)

// partialProvider reveals the first/last N characters of a value and
// masks the middle. Pure SQL: the expression handles NULL and short
// values without leaking length beyond what the visible fragments imply.
type partialProvider struct{}

func newPartialProvider() Provider { return &partialProvider{} }

// Type returns "partial".
func (partialProvider) Type() string { return "partial" }

// MaskSQL emits a CASE expression over the placeholder column. Every
// argument binds as a positional parameter — nothing from Args is
// interpolated into the snippet text.
func (partialProvider) MaskSQL(ctx MaskContext) (string, []Param, error) {
	mc := ctx.Args.MaskChar
	if mc == "" {
		mc = "*"
	}
	return "CASE WHEN __col__ IS NULL THEN NULL ELSE concat(" +
			"substr(__col__::VARCHAR, 1, $1), " +
			"repeat($2, greatest(length(__col__::VARCHAR) - $1 - $3, 0)), " +
			"substr(__col__::VARCHAR, greatest(length(__col__::VARCHAR) - $3 + 1, $1 + 1))) END",
		[]Param{
			{Name: "show_first", Value: ctx.Args.ShowFirst},
			{Name: "mask_char", Value: mc},
			{Name: "show_last", Value: ctx.Args.ShowLast},
		}, nil
}

// MaskArrow is not supported by this provider.
func (partialProvider) MaskArrow(_ MaskArrowContext) error {
	return ErrSQLOnly
}

// ValidateArgs requires non-negative show windows and a single-rune mask
// character (defaulted to "*" when empty).
func (partialProvider) ValidateArgs(a Args) error {
	if a.ShowFirst < 0 {
		return fmt.Errorf("%w: showFirst must be >= 0, got %d", ErrInvalidArgs, a.ShowFirst)
	}
	if a.ShowLast < 0 {
		return fmt.Errorf("%w: showLast must be >= 0, got %d", ErrInvalidArgs, a.ShowLast)
	}
	if a.MaskChar != "" && utf8.RuneCountInString(a.MaskChar) != 1 {
		return fmt.Errorf("%w: maskChar must be a single character, got %q", ErrInvalidArgs, a.MaskChar)
	}
	return nil
}
