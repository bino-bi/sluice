// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type staticSaltStore map[string][]byte

func (s staticSaltStore) Get(_ context.Context, ref string) ([]byte, error) {
	v, ok := s[ref]
	if !ok {
		return nil, errors.New("unknown salt ref")
	}
	return v, nil
}

func TestPartialProvider(t *testing.T) {
	t.Parallel()
	p := newPartialProvider()
	if p.Type() != "partial" {
		t.Errorf("Type() = %q", p.Type())
	}
	sql, params, err := p.MaskSQL(MaskContext{Args: Args{ShowFirst: 2, ShowLast: 1}})
	if err != nil {
		t.Fatalf("MaskSQL: %v", err)
	}
	if !strings.Contains(sql, "__col__") || !strings.Contains(sql, "$1") {
		t.Errorf("snippet missing placeholder markers: %q", sql)
	}
	if len(params) != 3 {
		t.Fatalf("params = %v, want 3 entries", params)
	}
	if params[1].Value != "*" {
		t.Errorf("default mask char = %v, want *", params[1].Value)
	}

	if err := p.ValidateArgs(Args{ShowFirst: -1}); err == nil {
		t.Error("negative showFirst accepted")
	}
	if err := p.ValidateArgs(Args{MaskChar: "ab"}); err == nil {
		t.Error("multi-rune maskChar accepted")
	}
	if err := p.ValidateArgs(Args{ShowFirst: 2, ShowLast: 2}); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}
}

func TestHashProvider(t *testing.T) {
	t.Parallel()
	p := newHashProvider()

	sql, params, err := p.MaskSQL(MaskContext{Args: Args{}})
	if err != nil {
		t.Fatalf("MaskSQL: %v", err)
	}
	if sql != "sha256(__col__::VARCHAR)" || len(params) != 0 {
		t.Errorf("unsalted snippet = %q params=%v", sql, params)
	}

	salts := staticSaltStore{"secret://env/SALT": []byte("pepper")}
	sql, params, err = p.MaskSQL(MaskContext{
		Ctx:       context.Background(),
		Args:      Args{SaltRef: "secret://env/SALT"},
		SaltStore: salts,
	})
	if err != nil {
		t.Fatalf("salted MaskSQL: %v", err)
	}
	if !strings.Contains(sql, "concat($1") {
		t.Errorf("salted snippet = %q, want salt bound as $1", sql)
	}
	if len(params) != 1 || params[0].Value != "pepper" {
		t.Errorf("salt param = %v", params)
	}

	// Missing store and unknown ref fail closed.
	if _, _, err := p.MaskSQL(MaskContext{Args: Args{SaltRef: "secret://env/SALT"}}); err == nil {
		t.Error("saltRef without store accepted")
	}
	if _, _, err := p.MaskSQL(MaskContext{Ctx: context.Background(), Args: Args{SaltRef: "secret://env/OTHER"}, SaltStore: salts}); err == nil {
		t.Error("unknown salt ref accepted")
	}

	if err := p.ValidateArgs(Args{Algorithm: "hmac_sha256"}); err == nil {
		t.Error("hmac_sha256 accepted before the post-query path exists")
	}
	if err := p.ValidateArgs(Args{Algorithm: "md5"}); err == nil {
		t.Error("unknown algorithm accepted")
	}
	if err := p.ValidateArgs(Args{}); err != nil {
		t.Errorf("default algorithm rejected: %v", err)
	}
}

func TestRegexProvider(t *testing.T) {
	t.Parallel()
	p := newRegexProvider()
	sql, params, err := p.MaskSQL(MaskContext{Args: Args{Pattern: `\d`, Replacement: "#"}})
	if err != nil {
		t.Fatalf("MaskSQL: %v", err)
	}
	if !strings.HasPrefix(sql, "regexp_replace(") || len(params) != 2 {
		t.Errorf("snippet = %q params = %v", sql, params)
	}

	if err := p.ValidateArgs(Args{}); err == nil {
		t.Error("empty pattern accepted")
	}
	if err := p.ValidateArgs(Args{Pattern: "("}); err == nil {
		t.Error("invalid regex accepted")
	}
	if err := p.ValidateArgs(Args{Pattern: strings.Repeat("a", maxRegexPatternLen+1)}); err == nil {
		t.Error("oversized pattern accepted")
	}
	if err := p.ValidateArgs(Args{Pattern: `\d+`, Replacement: ""}); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}
}

func TestTruncateProvider(t *testing.T) {
	t.Parallel()
	p := newTruncateProvider()
	sql, params, err := p.MaskSQL(MaskContext{Args: Args{Length: 8, Suffix: "…"}})
	if err != nil {
		t.Fatalf("MaskSQL: %v", err)
	}
	if !strings.Contains(sql, "substr(__col__::VARCHAR, 1, $1)") || len(params) != 2 {
		t.Errorf("snippet = %q params = %v", sql, params)
	}

	if err := p.ValidateArgs(Args{Length: 0}); err == nil {
		t.Error("zero length accepted")
	}
	if err := p.ValidateArgs(Args{Length: 10}); err != nil {
		t.Errorf("valid args rejected: %v", err)
	}
}

func TestDefaultRegistryHasSQLProviders(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"null", "constant", "partial", "hash", "regex", "truncate"} {
		if _, ok := Default().Lookup(typ); !ok {
			t.Errorf("Default() missing provider %q", typ)
		}
	}
}
