// SPDX-License-Identifier: AGPL-3.0-or-later

package identity

import (
	stderrors "errors"
	"log/slog"
	"net/http"

	apierrors "github.com/bino-bi/sluice/pkg/errors"
)

// MiddlewareOptions configures the HTTP middleware factory.
type MiddlewareOptions struct {
	// Identifier is the composite or concrete identifier run against
	// every request. Required.
	Identifier Identifier

	// AllowAnonymous lets requests through without a UserCtx when
	// Identifier returns ErrNoCredential. MVP default is false — any
	// unauthenticated request receives 401.
	AllowAnonymous bool

	// Logger receives slog messages for rejected requests. Nil uses
	// slog.Default.
	Logger *slog.Logger
}

// Middleware returns an HTTP middleware that runs Identifier for every
// request and populates the UserCtx on the request context. Non-nil
// Identifier is required; the middleware panics on nil input so
// composition bugs surface at startup.
func Middleware(opts MiddlewareOptions) func(http.Handler) http.Handler {
	if opts.Identifier == nil {
		panic("identity.Middleware: Identifier is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uc, err := opts.Identifier.Identify(r.Context(), r)
			if err != nil {
				if stderrors.Is(err, ErrNoCredential) && opts.AllowAnonymous {
					next.ServeHTTP(w, r)
					return
				}
				writeAuthnError(w, r, log, err)
				return
			}
			ctx := WithUser(r.Context(), uc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeAuthnError emits a 401 body matching pkg/errors.APIError. The
// concrete error detail is logged but not leaked to the client.
func writeAuthnError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	log.WarnContext(r.Context(), "identity: authentication failed",
		slog.String("remote", r.RemoteAddr),
		slog.String("path", r.URL.Path),
		slog.String("error", err.Error()),
	)
	_ = err // detail is logged above; code is always 401 for MVP.
	code := apierrors.CodeUnauthorized
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="sluice", charset="UTF-8"`)
	w.WriteHeader(apierrors.Status(code))
	_, _ = w.Write([]byte(`{"code":"` + string(code) + `","message":"` + apierrors.Message(code) + `"}`))
}
