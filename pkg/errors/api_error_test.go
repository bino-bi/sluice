// SPDX-License-Identifier: Apache-2.0

package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"
)

func TestNewCarriesCanonicalMessageAndStatus(t *testing.T) {
	t.Parallel()
	e := New(CodeACLDenied)
	if e.Code != CodeACLDenied {
		t.Errorf("Code = %q, want %q", e.Code, CodeACLDenied)
	}
	if e.Message != defaultMessage[CodeACLDenied] {
		t.Errorf("Message = %q, want canonical", e.Message)
	}
	if e.Status() != httpStatusByCode[CodeACLDenied] {
		t.Errorf("Status = %d, want %d", e.Status(), httpStatusByCode[CodeACLDenied])
	}
}

func TestNewfFormatsMessage(t *testing.T) {
	t.Parallel()
	e := Newf(CodeSyntax, "unexpected %s at position %d", "FROM", 42)
	want := "unexpected FROM at position 42"
	if e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
}

func TestBuildersDoNotMutateOriginal(t *testing.T) {
	t.Parallel()
	orig := New(CodeACLDenied)
	_ = orig.WithMessage("custom").WithQueryID("Q1").WithPolicy("P1").WithDetail("k", "v")

	if orig.Message != defaultMessage[CodeACLDenied] {
		t.Errorf("original Message mutated: %q", orig.Message)
	}
	if orig.QueryID != "" {
		t.Errorf("original QueryID mutated: %q", orig.QueryID)
	}
	if orig.Policy != "" {
		t.Errorf("original Policy mutated: %q", orig.Policy)
	}
	if len(orig.Details) != 0 {
		t.Errorf("original Details mutated: %v", orig.Details)
	}
}

func TestWithDetailClonesMap(t *testing.T) {
	t.Parallel()
	base := New(CodeACLRejected).WithDetail("a", 1)
	branch1 := base.WithDetail("b", 2)
	branch2 := base.WithDetail("c", 3)

	if _, ok := branch1.Details["c"]; ok {
		t.Error("branch1 should not see branch2's key")
	}
	if _, ok := branch2.Details["b"]; ok {
		t.Error("branch2 should not see branch1's key")
	}
	if len(base.Details) != 1 {
		t.Errorf("base Details mutated: %v", base.Details)
	}
}

func TestErrorString(t *testing.T) {
	t.Parallel()
	e := New(CodeSyntax).WithMessage("bad SQL")
	want := "ERR_SYNTAX: bad SQL"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	t.Parallel()
	in := New(CodeACLDenied).
		WithQueryID("01HY0000000000000000000000").
		WithPolicy("finance-ro").
		WithDetail("table", "public.customers")

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out APIError
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Code != in.Code || out.Message != in.Message ||
		out.QueryID != in.QueryID || out.Policy != in.Policy {
		t.Errorf("round-trip mismatch: in=%+v out=%+v", in, out)
	}
	if got, want := out.Details["table"], "public.customers"; got != want {
		t.Errorf("Details[table] = %v, want %v", got, want)
	}
	// status is unexported and must not appear in JSON.
	if bytes := string(b); contains(bytes, `"status"`) {
		t.Errorf("status should not be in JSON: %s", bytes)
	}
}

func TestWrapPreservesCauseAndUnwrap(t *testing.T) {
	t.Parallel()
	cause := fmt.Errorf("underlying problem")
	e := Wrap(CodeInternal, cause)
	if !stderrors.Is(e, cause) {
		t.Error("errors.Is should match wrapped cause")
	}
	// Verify single-level Unwrap returns the direct cause by pointer identity.
	// errorlint flags raw comparison, but this is exactly what we're testing.
	//nolint:errorlint
	if e.Unwrap() != cause {
		t.Error("Unwrap should return the direct cause")
	}
}

func TestIsMatchesByCode(t *testing.T) {
	t.Parallel()
	a := New(CodeACLDenied).WithPolicy("p1")
	b := New(CodeACLDenied).WithPolicy("p2")
	if !stderrors.Is(a, b) {
		t.Error("same code should compare equal via errors.Is")
	}
	c := New(CodeSyntax)
	if stderrors.Is(a, c) {
		t.Error("different codes must not match")
	}
}

func TestFromErrorOnAPIError(t *testing.T) {
	t.Parallel()
	in := New(CodeACLRejected).WithPolicy("finance-ro")
	got := FromError(fmt.Errorf("wrapped: %w", in))
	if got.Code != CodeACLRejected {
		t.Errorf("Code = %q, want %q", got.Code, CodeACLRejected)
	}
	if got.Policy != "finance-ro" {
		t.Errorf("Policy = %q, want finance-ro", got.Policy)
	}
}

func TestFromErrorOnPlainError(t *testing.T) {
	t.Parallel()
	got := FromError(fmt.Errorf("some random thing"))
	if got.Code != CodeInternal {
		t.Errorf("Code = %q, want CodeInternal", got.Code)
	}
	if got.Details != nil {
		t.Errorf("Details should be nil for internal default, got %v", got.Details)
	}
}

func TestFromErrorOnNil(t *testing.T) {
	t.Parallel()
	if got := FromError(nil); got != nil {
		t.Errorf("FromError(nil) = %+v, want nil", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
