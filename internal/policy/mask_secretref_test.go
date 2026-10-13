// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// A saltRef or keyRef must be a secret reference when the policy loads.
// Before, a plain text loaded, and every query on the table failed when the
// mask was built.
func TestCompile_MaskSecretRefForm(t *testing.T) {
	mask := func(spec apitypes.MaskSpec) *apitypes.ColumnMaskPolicy {
		return &apitypes.ColumnMaskPolicy{
			TypeMeta: apitypes.TypeMeta{APIVersion: apitypes.GroupVersionAlpha1, Kind: apitypes.KindColumnMaskPolicy},
			Metadata: apitypes.ObjectMeta{Name: "mask", Priority: 50},
			Spec: apitypes.ColumnMaskSpec{
				Match: apitypes.Selector{Any: []apitypes.Clause{{Resources: &apitypes.ResourceSelector{Tables: []string{"orders"}, Columns: []string{"email"}}}}},
				Mask:  spec,
			},
		}
	}
	hash := func(algo apitypes.HashAlgo, salt string) apitypes.MaskSpec {
		return apitypes.MaskSpec{Type: apitypes.MaskHash, Args: apitypes.MaskArgs{Algorithm: algo, SaltRef: salt}}
	}
	fpe := func(key string) apitypes.MaskSpec {
		return apitypes.MaskSpec{Type: apitypes.MaskFPE, Args: apitypes.MaskArgs{KeyRef: key, Alphabet: "numeric"}}
	}
	cases := []struct {
		name    string
		spec    apitypes.MaskSpec
		wantErr string
	}{
		{"sha256 without salt", hash("sha256", ""), ""},
		{"sha256 with a reference", hash("sha256", "secret://env/SALT"), ""},
		{"sha256 with plain text", hash("sha256", "pepper123"), "saltRef"},
		{"hmac with plain text", hash(apitypes.HashHMACSHA256, "pepper123"), "saltRef"},
		{"hmac with another scheme", hash(apitypes.HashHMACSHA256, "env://SALT"), "saltRef"},
		{"hmac with a provider that does not resolve", hash(apitypes.HashHMACSHA256, "secret://vault/kv/salt"), "saltRef"},
		{"hmac with a file reference", hash(apitypes.HashHMACSHA256, "secret://file//run/secrets/salt"), ""},
		{"fpe with plain text", fpe("0123456789abcdef"), "keyRef"},
		{"fpe with a reference", fpe("secret://env/FPE_KEY"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := New(Options{}).ApplySnapshot(context.Background(), makeSnapshot(allowAll(0), mask(tc.spec)))
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("apply: %v, want no error", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("apply: %v, want an error that names %s", err, tc.wantErr)
			}
		})
	}
}
