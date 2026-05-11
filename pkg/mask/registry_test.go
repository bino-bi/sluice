// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"errors"
	"slices"
	"testing"
)

func TestDefaultContainsBuiltins(t *testing.T) {
	t.Parallel()
	r := Default()
	for _, want := range []string{"null", "constant"} {
		if _, ok := r.Lookup(want); !ok {
			t.Errorf("Default() missing built-in %q", want)
		}
	}
}

func TestRegistryRegisterRejectsDuplicates(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(newNullProvider()); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := r.Register(newNullProvider())
	if !errors.Is(err, ErrDuplicateType) {
		t.Errorf("duplicate Register: got %v, want ErrDuplicateType", err)
	}
}

func TestRegistryTypesIsSorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	_ = r.Register(newConstantProvider())
	_ = r.Register(newNullProvider())
	got := r.Types()
	want := []string{"constant", "null"}
	if !slices.Equal(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
}

func TestRegistryLookupMissing(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if p, ok := r.Lookup("nope"); ok || p != nil {
		t.Errorf("Lookup missing = (%v, %v), want (nil, false)", p, ok)
	}
}
