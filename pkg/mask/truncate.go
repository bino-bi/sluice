// SPDX-License-Identifier: Apache-2.0

package mask

import "fmt"

// truncateProvider shortens values beyond Length characters, appending an
// optional suffix. Values at or under the limit pass through unchanged.
type truncateProvider struct{}

func newTruncateProvider() Provider { return &truncateProvider{} }

// Type returns "truncate".
func (truncateProvider) Type() string { return "truncate" }

// MaskSQL emits a CASE expression; length and suffix bind as parameters.
func (truncateProvider) MaskSQL(ctx MaskContext) (string, []Param, error) {
	return "CASE WHEN __col__ IS NULL THEN NULL " +
			"WHEN length(__col__::VARCHAR) > $1 THEN concat(substr(__col__::VARCHAR, 1, $1), $2) " +
			"ELSE __col__::VARCHAR END",
		[]Param{
			{Name: "length", Value: ctx.Args.Length},
			{Name: "suffix", Value: ctx.Args.Suffix},
		}, nil
}

// MaskArrow is not supported by this provider.
func (truncateProvider) MaskArrow(_ MaskArrowContext) error {
	return ErrSQLOnly
}

// ValidateArgs requires a positive length.
func (truncateProvider) ValidateArgs(a Args) error {
	if a.Length < 1 {
		return fmt.Errorf("%w: length must be >= 1, got %d", ErrInvalidArgs, a.Length)
	}
	return nil
}
