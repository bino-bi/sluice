// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/capitalone/fpe/ff1"
)

type staticKeyStore map[string][]byte

func (s staticKeyStore) Get(_ context.Context, ref string) ([]byte, error) {
	return staticSaltStore(s).Get(context.Background(), ref)
}

func TestFPEProvider_RoundTrip(t *testing.T) {
	t.Parallel()
	p := newFPEProvider().(RowMasker)
	key := []byte("0123456789abcdef") // 16 bytes
	keys := staticKeyStore{"secret://env/K": key}

	if IsPostQuery(newFPEProvider(), Args{}) != true {
		t.Fatal("fpe should route post-query")
	}

	args := Args{KeyRef: "secret://env/K", Alphabet: "numeric"}
	if err := newFPEProvider().ValidateArgs(args); err != nil {
		t.Fatalf("ValidateArgs: %v", err)
	}
	rm, err := p.NewRowMask(RowMaskContext{Ctx: context.Background(), Args: args, Keys: keys})
	if err != nil {
		t.Fatalf("NewRowMask: %v", err)
	}

	in := "123-45-6789"
	out, err := rm.Mask(in)
	if err != nil {
		t.Fatalf("Mask: %v", err)
	}
	enc := out.(string)
	if enc == in {
		t.Fatal("ciphertext equals plaintext")
	}
	// Format preserved: separators intact, digits stay digits.
	if len(enc) != len(in) || enc[3] != '-' || enc[6] != '-' {
		t.Fatalf("format not preserved: %q", enc)
	}

	// Independently decrypt with the same FF1 params to prove correctness.
	cipher, err := ff1.NewCipher(10, fpeMaxTweakLen, key, nil)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	dec, err := cipher.Decrypt(strings.ReplaceAll(enc, "-", ""))
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != "123456789" {
		t.Fatalf("decrypt = %q, want 123456789", dec)
	}

	// nil passes through.
	if v, _ := rm.Mask(nil); v != nil {
		t.Errorf("nil masked to %v", v)
	}
}

func TestFPEProvider_Determinism(t *testing.T) {
	t.Parallel()
	p := newFPEProvider().(RowMasker)
	keys := staticKeyStore{"secret://env/K": []byte("0123456789abcdef")}
	rm, _ := p.NewRowMask(RowMaskContext{Ctx: context.Background(), Args: Args{KeyRef: "secret://env/K", Alphabet: "numeric"}, Keys: keys})
	a, _ := rm.Mask("55555")
	b, _ := rm.Mask("55555")
	if a != b {
		t.Errorf("FPE not deterministic: %v vs %v", a, b)
	}
}

func TestFPEProvider_Validation(t *testing.T) {
	t.Parallel()
	p := newFPEProvider()
	if err := p.ValidateArgs(Args{}); err == nil {
		t.Error("missing keyRef accepted")
	}
	if err := p.ValidateArgs(Args{KeyRef: "k", Tweak: "zz"}); err == nil {
		t.Error("non-hex tweak accepted")
	}
	if err := p.ValidateArgs(Args{KeyRef: "k", Alphabet: "klingon"}); err == nil {
		t.Error("unknown alphabet accepted")
	}
	if err := p.ValidateArgs(Args{KeyRef: "k", CustomAlphabet: "aa"}); err == nil {
		t.Error("duplicate-char custom alphabet accepted")
	}
	if _, _, err := p.MaskSQL(MaskContext{}); !errors.Is(err, ErrPostQueryOnly) {
		t.Errorf("MaskSQL err = %v, want ErrPostQueryOnly", err)
	}
}

func TestJitterProvider(t *testing.T) {
	t.Parallel()
	p := newJitterProvider().(RowMasker)
	rm, err := p.NewRowMask(RowMaskContext{Ctx: context.Background(), Args: Args{Range: 0.1, Seed: "s"}})
	if err != nil {
		t.Fatalf("NewRowMask: %v", err)
	}
	a, err := rm.Mask(1000)
	if err != nil {
		t.Fatalf("Mask: %v", err)
	}
	ai := a.(int)
	if ai < 900 || ai > 1100 {
		t.Errorf("jittered %d outside ±10%%", ai)
	}
	// Deterministic.
	b, _ := rm.Mask(1000)
	if a != b {
		t.Errorf("jitter not deterministic: %v vs %v", a, b)
	}
	// Non-numeric fails closed.
	if _, err := rm.Mask("text"); err == nil {
		t.Error("jitter accepted non-numeric value")
	}
	if err := p.ValidateArgs(Args{Range: 0}); err == nil {
		t.Error("zero range accepted")
	}
	if err := p.ValidateArgs(Args{Range: 1.5}); err == nil {
		t.Error("range >= 1 accepted")
	}
}

func TestFakeProvider(t *testing.T) {
	t.Parallel()
	p := newFakeProvider().(RowMasker)
	for _, ft := range []string{"name", "email", "phone", "company", "city", "country", "uuid", "first_name", "last_name"} {
		rm, err := p.NewRowMask(RowMaskContext{Ctx: context.Background(), Args: Args{FakeType: ft, Seed: "s"}})
		if err != nil {
			t.Fatalf("NewRowMask %s: %v", ft, err)
		}
		a, err := rm.Mask("real-value")
		if err != nil {
			t.Fatalf("Mask %s: %v", ft, err)
		}
		if a == "real-value" || a == "" {
			t.Errorf("%s fake did not replace value: %v", ft, a)
		}
		// Deterministic and join-consistent.
		b, _ := rm.Mask("real-value")
		if a != b {
			t.Errorf("%s fake not deterministic", ft)
		}
	}
	if err := p.ValidateArgs(Args{}); err == nil {
		t.Error("missing fakeType accepted")
	}
	if err := p.ValidateArgs(Args{FakeType: "bogus"}); err == nil {
		t.Error("unknown fakeType accepted")
	}
}

func TestHashHMAC_PostQuery(t *testing.T) {
	t.Parallel()
	p := newHashProvider()
	if !IsPostQuery(p, Args{Algorithm: "hmac_sha256"}) {
		t.Error("hmac_sha256 should route post-query")
	}
	if IsPostQuery(p, Args{Algorithm: "sha256"}) {
		t.Error("sha256 should route SQL")
	}
	rm := p.(RowMasker)
	m, err := rm.NewRowMask(RowMaskContext{
		Ctx:   context.Background(),
		Args:  Args{Algorithm: "hmac_sha256", SaltRef: "secret://env/K"},
		Salts: staticSaltStore{"secret://env/K": []byte("key")},
	})
	if err != nil {
		t.Fatalf("NewRowMask: %v", err)
	}
	out, err := m.Mask("value")
	if err != nil {
		t.Fatalf("Mask: %v", err)
	}
	if s, ok := out.(string); !ok || len(s) != 64 {
		t.Errorf("hmac output = %v, want 64-hex", out)
	}
	if err := p.ValidateArgs(Args{Algorithm: "hmac_sha256"}); err == nil {
		t.Error("hmac_sha256 without saltRef accepted")
	}
}
