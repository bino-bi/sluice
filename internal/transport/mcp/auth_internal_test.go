// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bino-bi/sluice/internal/identity"
)

type fakeIdentifier struct {
	user *identity.UserCtx
	err  error
}

func (f *fakeIdentifier) Identify(_ context.Context, _ *http.Request) (*identity.UserCtx, error) {
	return f.user, f.err
}
func (f *fakeIdentifier) Name() string { return "fake" }

func newServerForAuth(id identity.Identifier, allowAnon bool) *Server {
	return &Server{
		cfg:  Config{AllowAnonymous: allowAnon},
		deps: Deps{Identifier: id},
		lg:   slog.Default(),
	}
}

func TestToolWhoAmI(t *testing.T) {
	s := &Server{}

	// Anonymous context.
	_, out, err := s.toolWhoAmI(context.Background(), nil, WhoAmIArgs{})
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !out.Anonymous {
		t.Errorf("expected anonymous for a context with no user")
	}

	// Authenticated context.
	ctx := identity.WithUser(context.Background(), &identity.UserCtx{
		Subject:    "alice",
		Issuer:     "https://idp.example",
		Groups:     []string{"analytics"},
		AuthMethod: identity.AuthMethodAPIKey,
	})
	_, out2, err := s.toolWhoAmI(ctx, nil, WhoAmIArgs{})
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if out2.Anonymous || out2.Subject != "alice" || out2.Issuer != "https://idp.example" {
		t.Errorf("unexpected identity: %+v", out2)
	}
	if len(out2.Groups) != 1 || out2.Groups[0] != "analytics" {
		t.Errorf("groups = %v, want [analytics]", out2.Groups)
	}
}

func TestAuthMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		id         identity.Identifier
		allowAnon  bool
		wantStatus int
		wantUser   string // "" means no user expected
	}{
		{
			name:       "valid credential passes with user",
			id:         &fakeIdentifier{user: &identity.UserCtx{Subject: "alice"}},
			wantStatus: http.StatusOK,
			wantUser:   "alice",
		},
		{
			name:       "missing credential is 401 when fail-closed",
			id:         &fakeIdentifier{err: identity.ErrNoCredential},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing credential passes anonymous when allowed",
			id:         &fakeIdentifier{err: identity.ErrNoCredential},
			allowAnon:  true,
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid credential is always 401 even when anonymous allowed",
			id:         &fakeIdentifier{err: identity.ErrInvalidCredential},
			allowAnon:  true,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "no identifier configured passes through",
			id:         nil,
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServerForAuth(tc.id, tc.allowAnon)
			var gotUser string
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if u, ok := userFrom(r.Context()); ok && u != nil {
					gotUser = u.Subject
				}
				w.WriteHeader(http.StatusOK)
			})
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			s.authMiddleware(next).ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				if got := rr.Header().Get("WWW-Authenticate"); got == "" {
					t.Errorf("401 must carry WWW-Authenticate header")
				}
			}
			if gotUser != tc.wantUser {
				t.Errorf("user = %q, want %q", gotUser, tc.wantUser)
			}
		})
	}
}
