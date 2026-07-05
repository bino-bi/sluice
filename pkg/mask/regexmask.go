// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"fmt"
	"regexp"
)

// maxRegexPatternLen bounds mask patterns; anything longer is almost
// certainly a mistake and risks pathological engine behaviour.
const maxRegexPatternLen = 512

// regexProvider rewrites matching substrings via DuckDB's regexp_replace.
// DuckDB uses RE2 — the same engine as Go's regexp — so ValidateArgs can
// compile the pattern at policy-load time with matching semantics.
type regexProvider struct{}

func newRegexProvider() Provider { return &regexProvider{} }

// Type returns "regex".
func (regexProvider) Type() string { return "regex" }

// MaskSQL binds pattern and replacement as parameters; the 'g' flag makes
// the replacement global, matching operator expectations for masking.
func (regexProvider) MaskSQL(ctx MaskContext) (string, []Param, error) {
	return "regexp_replace(__col__::VARCHAR, $1, $2, 'g')",
		[]Param{
			{Name: "pattern", Value: ctx.Args.Pattern},
			{Name: "replacement", Value: ctx.Args.Replacement},
		}, nil
}

// MaskArrow is not supported by this provider.
func (regexProvider) MaskArrow(_ MaskArrowContext) error {
	return ErrSQLOnly
}

// ValidateArgs compiles the pattern (RE2) and bounds its length. An empty
// replacement is fine — it deletes the matches.
func (regexProvider) ValidateArgs(a Args) error {
	if a.Pattern == "" {
		return fmt.Errorf("%w: pattern required", ErrInvalidArgs)
	}
	if len(a.Pattern) > maxRegexPatternLen {
		return fmt.Errorf("%w: pattern longer than %d bytes", ErrInvalidArgs, maxRegexPatternLen)
	}
	if _, err := regexp.Compile(a.Pattern); err != nil {
		return fmt.Errorf("%w: pattern: %w", ErrInvalidArgs, err)
	}
	return nil
}
