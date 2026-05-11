// SPDX-License-Identifier: AGPL-3.0-or-later

package policy_test

import (
	"errors"
	"testing"

	"github.com/bino-bi/sluice/internal/policy"
)

// FuzzTemplate asserts that CompileTemplate is robust to arbitrary input:
// it either returns a well-formed Template (if the input happens to parse)
// or an error that wraps policy.ErrTemplateInvalid. Any other outcome —
// panic, nil template without error, non-sentinel error — is a bug the
// conflict resolver or rewriter would expose later.
func FuzzTemplate(f *testing.F) {
	seeds := []string{
		"",
		"{{ subject.sub }}",
		"{{subject.sub}}",
		"{{ subject.jwt.tenant_id }}",
		"{{ request.remote_ip }}",
		"literal",
		"prefix-{{ subject.sub }}-suffix",
		"{{ }}",
		"{{ . }}",
		"{{ subject..sub }}",
		"{{ subject.sub",
		"subject.sub }}",
		"{{ {{ nested }} }}",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		tpl, err := policy.CompileTemplate(raw)
		if err != nil {
			if !errors.Is(err, policy.ErrTemplateInvalid) {
				t.Fatalf("non-sentinel error from CompileTemplate(%q): %v", raw, err)
			}
			return
		}
		if tpl == nil {
			t.Fatalf("nil template with nil error for %q", raw)
		}
		if len(tpl.Path) == 0 {
			t.Fatalf("compiled template has empty path for %q", raw)
		}
	})
}
