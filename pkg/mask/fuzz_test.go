// SPDX-License-Identifier: Apache-2.0

package mask_test

import (
	"errors"
	"testing"

	"github.com/bino-bi/sluice/pkg/mask"
)

// FuzzValidateArgs exercises the constant + null providers across a range
// of Args shapes. The only accepted return types are nil and errors that
// wrap mask.ErrInvalidArgs — anything else leaks an unclassified failure
// mode to policy load, which would surface as a generic ERR_POLICY_INVALID
// without actionable detail.
func FuzzValidateArgs(f *testing.F) {
	seeds := []struct {
		value     string
		showFirst int
		showLast  int
		algo      string
	}{
		{"", 0, 0, ""},
		{"***", 2, 2, "sha256"},
		{"redacted", 0, 4, "hmac_sha256"},
	}
	for _, s := range seeds {
		f.Add(s.value, s.showFirst, s.showLast, s.algo)
	}
	reg := mask.Default()
	providers := []string{"null", "constant"}

	f.Fuzz(func(t *testing.T, value string, showFirst, showLast int, algo string) {
		args := mask.Args{
			Value:     value,
			ShowFirst: showFirst,
			ShowLast:  showLast,
			Algorithm: algo,
		}
		for _, name := range providers {
			p, ok := reg.Lookup(name)
			if !ok {
				t.Fatalf("provider %q missing from Default registry", name)
			}
			err := p.ValidateArgs(args)
			if err == nil {
				continue
			}
			if !errors.Is(err, mask.ErrInvalidArgs) {
				t.Fatalf("%s.ValidateArgs returned non-sentinel error: %v", name, err)
			}
		}
	})
}
