// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"30", 30 * time.Second},
		{"30s", 30 * time.Second},
		{"1h30m", 90 * time.Minute},
		{"500ms", 500 * time.Millisecond},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDuration(c.in)
			if err != nil {
				t.Fatalf("ParseDuration(%q): %v", c.in, err)
			}
			if got.Duration() != c.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", c.in, got.Duration(), c.want)
			}
		})
	}
}

func TestParseDurationRejectsNegative(t *testing.T) {
	t.Parallel()
	cases := []string{"-1", "-30s", "-1h"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseDuration(c); err == nil {
				t.Errorf("ParseDuration(%q) should fail for negative", c)
			}
		})
	}
}

func TestParseDurationRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := ParseDuration("not a duration"); err == nil {
		t.Error("ParseDuration should fail on garbage input")
	}
}

func TestDurationJSONRoundTrip(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		D Duration `json:"d"`
	}

	cases := []string{"30s", "1h30m"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			t.Parallel()
			parsed, err := ParseDuration(c)
			if err != nil {
				t.Fatal(err)
			}
			in := wrapper{D: parsed}
			b, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			var out wrapper
			if err := json.Unmarshal(b, &out); err != nil {
				t.Fatal(err)
			}
			if out.D != in.D {
				t.Errorf("round-trip mismatch: in=%v bytes=%s out=%v", in.D, b, out.D)
			}
		})
	}
}

func TestDurationJSONAcceptsNumber(t *testing.T) {
	t.Parallel()
	var d Duration
	if err := d.UnmarshalJSON([]byte("30")); err != nil {
		t.Fatal(err)
	}
	if d.Duration() != 30*time.Second {
		t.Errorf("UnmarshalJSON(30) = %v, want 30s", d.Duration())
	}
}
