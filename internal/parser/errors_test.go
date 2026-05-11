// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import (
	"errors"
	"testing"
)

func TestParseErrorIsErrSyntax(t *testing.T) {
	var pe error = &ParseError{Line: 3, Col: 7, Cause: errors.New("bad token")}
	if !errors.Is(pe, ErrSyntax) {
		t.Fatalf("errors.Is(*ParseError, ErrSyntax) = false; want true")
	}
}

func TestParseErrorUnwrap(t *testing.T) {
	cause := errors.New("bad token")
	pe := &ParseError{Cause: cause}
	if got := errors.Unwrap(pe); !errors.Is(got, cause) {
		t.Fatalf("Unwrap = %v; want %v", got, cause)
	}
}

func TestParseErrorFormatting(t *testing.T) {
	cases := []struct {
		name string
		err  *ParseError
		want string
	}{
		{
			name: "with position",
			err:  &ParseError{Line: 3, Col: 7, Cause: errors.New("boom")},
			want: "parser: syntax error at line 3 col 7: boom",
		},
		{
			name: "without position",
			err:  &ParseError{Cause: errors.New("boom")},
			want: "parser: syntax error: boom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("Error() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestStmtKindIsReadOnly(t *testing.T) {
	readOnly := []StmtKind{StmtSelect, StmtExplain, StmtSet, StmtShow, StmtPragma}
	writing := []StmtKind{StmtInsert, StmtUpdate, StmtDelete, StmtDDL, StmtCopy, StmtAttach, StmtLoad, StmtInstall, StmtUnsupported}

	for _, k := range readOnly {
		if !k.IsReadOnly() {
			t.Errorf("%s.IsReadOnly() = false; want true", k)
		}
	}
	for _, k := range writing {
		if k.IsReadOnly() {
			t.Errorf("%s.IsReadOnly() = true; want false", k)
		}
	}
}
