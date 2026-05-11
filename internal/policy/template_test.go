// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"errors"
	"net"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

func TestLooksLikeTemplate(t *testing.T) {
	cases := map[string]bool{
		"{{ subject.sub }}":  true,
		"plain string":       false,
		"only one {{ side":   false,
		"only }} right side": false,
		"":                   false,
	}
	for in, want := range cases {
		if got := looksLikeTemplate(in); got != want {
			t.Errorf("looksLikeTemplate(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCompileTemplate(t *testing.T) {
	tpl, err := CompileTemplate("{{ subject.jwt.tenant_id }}")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	want := []string{"subject", "jwt", "tenant_id"}
	if len(tpl.Path) != len(want) {
		t.Fatalf("path len = %d, want %d (%v)", len(tpl.Path), len(want), tpl.Path)
	}
	for i := range want {
		if tpl.Path[i] != want[i] {
			t.Errorf("path[%d] = %q, want %q", i, tpl.Path[i], want[i])
		}
	}

	if _, err := CompileTemplate("prefix-{{ subject.sub }}"); err == nil {
		t.Error("mixed template must fail")
	}
	if _, err := CompileTemplate("{{   }}"); err == nil {
		t.Error("empty ref must fail")
	}
	if _, err := CompileTemplate("{{ a..b }}"); err == nil {
		t.Error("empty segment must fail")
	}
	if _, err := CompileTemplate("no template here"); err == nil {
		t.Error("non-template must fail CompileTemplate")
	}
}

func TestTemplateRender_Subject(t *testing.T) {
	u := &identity.UserCtx{
		Subject: "alice",
		Email:   "alice@example.com",
		Groups:  []string{"ops", "bi-users"},
		Claims: map[string]any{
			"tenant_id": "t-42",
		},
	}

	tpl, err := CompileTemplate("{{ subject.sub }}")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := tpl.Render(u, nil)
	if err != nil {
		t.Fatalf("render sub: %v", err)
	}
	if got != "alice" {
		t.Errorf("subject.sub = %v, want %q", got, "alice")
	}

	tpl, _ = CompileTemplate("{{ subject.email }}")
	got, _ = tpl.Render(u, nil)
	if got != "alice@example.com" {
		t.Errorf("email = %v", got)
	}

	// subject.jwt.* is the namespaced accessor convention: "jwt" is a
	// prefix that means "look in user.Claims", it is not a literal claim
	// key. The subject.tenant_id shortcut produces the same value.
	tpl, _ = CompileTemplate("{{ subject.jwt.tenant_id }}")
	got, err = tpl.Render(u, nil)
	if err != nil {
		t.Fatalf("render jwt.tenant_id: %v", err)
	}
	if got != "t-42" {
		t.Errorf("jwt.tenant_id = %v, want t-42", got)
	}

	// Direct claim path (no jwt/claims prefix).
	tpl, _ = CompileTemplate("{{ subject.tenant_id }}")
	got, err = tpl.Render(u, nil)
	if err != nil {
		t.Fatalf("render tenant_id: %v", err)
	}
	if got != "t-42" {
		t.Errorf("direct claim = %v", got)
	}

	tpl, _ = CompileTemplate("{{ subject.missing }}")
	if _, err := tpl.Render(u, nil); !errors.Is(err, ErrTemplateVarMissing) {
		t.Errorf("missing claim: got %v, want ErrTemplateVarMissing", err)
	}
}

func TestTemplateRender_Request(t *testing.T) {
	facts := &RequestFacts{
		RemoteIP:  net.ParseIP("10.0.0.7"),
		UserAgent: "sluice-test/1.0",
		Headers:   map[string]string{"X-Tenant": "t-1"},
	}
	for input, want := range map[string]any{
		"{{ request.remote_ip }}":        "10.0.0.7",
		"{{ request.user_agent }}":       "sluice-test/1.0",
		"{{ request.headers.X-Tenant }}": "t-1",
	} {
		tpl, err := CompileTemplate(input)
		if err != nil {
			t.Fatalf("compile %q: %v", input, err)
		}
		got, err := tpl.Render(nil, facts)
		if err != nil {
			t.Fatalf("render %q: %v", input, err)
		}
		if got != want {
			t.Errorf("%q = %v, want %v", input, got, want)
		}
	}
}

func TestTemplateRender_RootUnknown(t *testing.T) {
	tpl, err := CompileTemplate("{{ bogus.path }}")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := tpl.Render(&identity.UserCtx{}, nil); !errors.Is(err, ErrTemplateVarMissing) {
		t.Errorf("got %v, want ErrTemplateVarMissing", err)
	}
}
