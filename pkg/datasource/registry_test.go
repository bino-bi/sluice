// SPDX-License-Identifier: Apache-2.0

package datasource

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestRegisterAndLookup(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	called := false
	factory := func(_ context.Context, _ Spec) (DataSource, error) {
		called = true
		return nil, nil
	}
	Register("fake", factory)

	f, ok := Lookup("fake")
	if !ok {
		t.Fatal("Lookup(fake) = (_, false), want true")
	}
	if _, err := f(context.Background(), Spec{}); err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !called {
		t.Error("factory was not invoked")
	}
}

func TestLookupMissing(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	if f, ok := Lookup("nope"); ok || f != nil {
		t.Errorf("Lookup(nope) = (%v, %v), want (nil, false)", f, ok)
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register("dup", func(_ context.Context, _ Spec) (DataSource, error) { return nil, nil })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register duplicate: expected panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is %T, want string: %v", r, r)
		}
		if !strings.Contains(msg, "dup") {
			t.Errorf("panic msg = %q, should mention type", msg)
		}
	}()
	Register("dup", func(_ context.Context, _ Spec) (DataSource, error) { return nil, nil })
}

func TestRegisterPanicsOnEmptyType(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty type")
		}
	}()
	Register("", func(_ context.Context, _ Spec) (DataSource, error) { return nil, nil })
}

func TestRegisterPanicsOnNilFactory(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil factory")
		}
	}()
	Register("has-nil-factory", nil)
}

func TestTypesSorted(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	noop := func(_ context.Context, _ Spec) (DataSource, error) { return nil, nil }
	Register("zeta", noop)
	Register("alpha", noop)
	Register("mu", noop)

	got := Types()
	want := []string{"alpha", "mu", "zeta"}
	if !slices.Equal(got, want) {
		t.Errorf("Types() = %v, want %v", got, want)
	}
}
