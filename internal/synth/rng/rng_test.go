// SPDX-License-Identifier: AGPL-3.0-or-later

package rng

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
)

var testSeed = []byte("0123456789abcdef-test-seed")

// TestGolden pins the first values of a fixed stream. A Go release that
// changes ChaCha8, or a change of the algorithms here, fails it and must
// bump Version.
func TestGolden(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("golden values are pinned on amd64; math.Log and math.Cos may differ in the last bit elsewhere")
	}
	k := Key(testSeed, "crmsynth", "crm.customers", "revenue", PurposeValues)
	if got, want := hex.EncodeToString(k[:]), "76db422d2637cbfdc32de72e9fb2b310a2a93c2f10a26eb0496ea225dbe9ca3a"; got != want {
		t.Errorf("Key = %s, want %s", got, want)
	}
	s := New(k)
	var got []string
	for range 3 {
		got = append(got, fmt.Sprintf("%#x", s.Uint64()))
	}
	for range 3 {
		got = append(got, fmt.Sprintf("%b", s.Float64()))
	}
	for range 3 {
		got = append(got, fmt.Sprint(s.IntN(1000)))
	}
	for range 3 {
		got = append(got, fmt.Sprintf("%b", s.Norm()))
	}
	want := []string{
		"0x716bca522f69bbbe", "0xaa8779cd7dabf0cb", "0xd4367ee51b9e22e8",
		"8740053805456448p-58", "8062733000251978p-54", "8741456854558776p-53",
		"400", "877", "219",
		"7124830159865545p-53", "5287786101516020p-55", "-6903748852114816p-52",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("values:\n got %q\nwant %q", got, want)
	}
}

func TestKeyMatchesSpec(t *testing.T) {
	m := hmac.New(sha256.New, testSeed)
	m.Write([]byte("sluice-synth/v1\x00n\x00t\x00c\x00values"))
	want := m.Sum(nil)
	got := Key(testSeed, "n", "t", "c", PurposeValues)
	if !hmac.Equal(got[:], want) {
		t.Fatalf("Key differs from HMAC-SHA256(seed, version NUL name NUL table NUL column NUL purpose)")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	base := Key(testSeed, "n", "t", "c", PurposeValues)
	others := [][32]byte{
		Key([]byte("another seed of 16+ bytes"), "n", "t", "c", PurposeValues),
		Key(testSeed, "m", "t", "c", PurposeValues),
		Key(testSeed, "n", "u", "c", PurposeValues),
		Key(testSeed, "n", "t", "d", PurposeValues),
		Key(testSeed, "n", "t", "c", PurposeNulls),
		// The separator keeps "a"+"bc" apart from "ab"+"c".
		Key(testSeed, "n", "tc", "", PurposeValues),
	}
	for i, o := range others {
		if o == base {
			t.Errorf("key %d equals the base key", i)
		}
	}
	if Key(testSeed, "n", "t", "c", PurposeValues) != base {
		t.Error("Key is not deterministic")
	}
}

func TestFloat64Range(t *testing.T) {
	s := For(testSeed, "n", "t", "c", PurposeValues)
	sum := 0.0
	const n = 100000
	for range n {
		f := s.Float64()
		if f < 0 || f >= 1 {
			t.Fatalf("Float64 = %v, want [0, 1)", f)
		}
		sum += f
	}
	if mean := sum / n; math.Abs(mean-0.5) > 0.01 {
		t.Errorf("mean = %v, want about 0.5", mean)
	}
}

func TestIntN(t *testing.T) {
	s := For(testSeed, "n", "t", "c", PurposeValues)
	counts := make([]int, 7)
	const n = 70000
	for range n {
		v := s.IntN(7)
		if v < 0 || v >= 7 {
			t.Fatalf("IntN(7) = %d", v)
		}
		counts[v]++
	}
	for i, c := range counts {
		if c < 9500 || c > 10500 {
			t.Errorf("bucket %d has %d draws, want about 10000", i, c)
		}
	}
	if got := s.IntN(1); got != 0 {
		t.Errorf("IntN(1) = %d", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("IntN(0) did not panic")
		}
	}()
	s.IntN(0)
}

func TestNorm(t *testing.T) {
	s := For(testSeed, "n", "t", "c", PurposeValues)
	const n = 100000
	sum, sq := 0.0, 0.0
	for range n {
		z := s.Norm()
		if math.IsNaN(z) || math.IsInf(z, 0) {
			t.Fatalf("Norm = %v", z)
		}
		sum += z
		sq += z * z
	}
	mean := sum / n
	sd := math.Sqrt(sq/n - mean*mean)
	if math.Abs(mean) > 0.02 || math.Abs(sd-1) > 0.02 {
		t.Errorf("mean %v, sd %v; want about 0 and 1", mean, sd)
	}
}

func TestStreamHidesState(t *testing.T) {
	s := For(testSeed, "n", "t", "c", PurposeValues)
	for _, f := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(f, s); got != "rng.Stream" {
			t.Errorf("Sprintf(%q) = %q", f, got)
		}
	}
}
