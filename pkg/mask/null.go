// SPDX-License-Identifier: Apache-2.0

package mask

// nullProvider replaces a column value with SQL NULL. It takes no args and
// supports only the SQL path.
type nullProvider struct{}

func newNullProvider() Provider { return &nullProvider{} }

// Type returns "null".
func (nullProvider) Type() string { return "null" }

// MaskSQL returns the literal SQL NULL. No params are required.
func (nullProvider) MaskSQL(_ MaskContext) (string, []Param, error) {
	return "NULL", nil, nil
}

// MaskArrow is not supported by this provider.
func (nullProvider) MaskArrow(_ MaskArrowContext) error {
	return ErrSQLOnly
}

// ValidateArgs accepts the zero Args. Any populated field is ignored
// silently because a NULL mask has no parameters.
func (nullProvider) ValidateArgs(_ Args) error { return nil }
