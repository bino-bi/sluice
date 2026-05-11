// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"context"
	"testing"
)

// The adapter constructors already return the public interfaces (see
// adapters.go), so the compile-time interface conformance is guaranteed by
// their signatures — no separate test needed.

func TestAdapters_Roundtrip(t *testing.T) {
	t.Setenv("SLUICE_ADAPTER_SECRET", "salty")
	r := NewResolver(ResolverOptions{})

	salt := NewSaltStore(r)
	got, err := salt.Get(context.Background(), "secret://env/SLUICE_ADAPTER_SECRET")
	if err != nil {
		t.Fatalf("SaltStore.Get: %v", err)
	}
	if string(got) != "salty" {
		t.Fatalf("value = %q", got)
	}

	ds := NewDataSourceResolver(r)
	got2, err := ds.Resolve(context.Background(), "secret://env/SLUICE_ADAPTER_SECRET")
	if err != nil {
		t.Fatalf("SecretResolver.Resolve: %v", err)
	}
	if string(got2) != "salty" {
		t.Fatalf("value = %q", got2)
	}
}
