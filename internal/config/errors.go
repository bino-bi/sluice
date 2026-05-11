// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// ValidationError aggregates per-document validation failures from a policy
// directory load. Each entry carries the source file and (best-effort) the
// line number so `sluice policy validate` can print actionable output. The
// line is 0 until JSON-Schema validation lands with the query-path slice;
// until then a document-level line is emitted if the decoder supplies one.
type ValidationError struct {
	File  string
	Line  int
	Kind  apitypes.Kind
	Name  string
	Msg   string
	Cause error
}

// Error implements error.
func (e *ValidationError) Error() string {
	var b strings.Builder
	if e.File != "" {
		b.WriteString(e.File)
		if e.Line > 0 {
			fmt.Fprintf(&b, ":%d", e.Line)
		}
		b.WriteString(": ")
	}
	if e.Kind != "" {
		b.WriteString(string(e.Kind))
		if e.Name != "" {
			b.WriteString("/")
			b.WriteString(e.Name)
		}
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// Unwrap returns the wrapped cause, if any.
func (e *ValidationError) Unwrap() error { return e.Cause }

// ValidationErrors aggregates multiple ValidationError values so LoadDirectory
// can surface every issue found across the directory in one call.
type ValidationErrors []*ValidationError

// Error implements error. The joined output is newline-separated.
func (es ValidationErrors) Error() string {
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "\n")
}

// Is reports true when any contained error matches target. This lets callers
// write `errors.Is(err, &ValidationError{})` to branch on validation failure.
func (es ValidationErrors) Is(target error) bool {
	var ve *ValidationError
	if errors.As(target, &ve) {
		return len(es) > 0
	}
	return false
}
