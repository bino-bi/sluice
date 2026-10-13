// SPDX-License-Identifier: AGPL-3.0-or-later

package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/bino-bi/sluice/internal/approval"
	"github.com/bino-bi/sluice/internal/identity"
)

// approvalPage is the answer to a GET of an accept or reject link. The form
// posts to the URL of the page, so the token stays in the query string.
// Nothing of the request is written into the page.
const approvalPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Sluice approval</title></head>
<body>
<p>%s this approval request?</p>
<form method="post"><button type="submit">%s</button></form>
</body>
</html>
`

// handleApprovalPage answers a GET of an accept or reject link with a page
// that asks for the decision. Chat clients, mail scanners and browsers
// fetch links on their own, so a GET must not decide. The page never
// reaches the broker and is the same for every id and token, so it is no
// existence or token oracle either.
func handleApprovalPage(accept bool) http.HandlerFunc {
	verb := "Reject"
	if accept {
		verb = "Accept"
	}
	page := []byte(fmt.Sprintf(approvalPage, verb, verb))
	return func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(page)
	}
}

// handleApprovalDecision handles a POST to the public accept/reject
// capability endpoints. The token in ?token= (or X-Approval-Token) is the
// sole authorisation. Unknown id and bad token return an identical 404 so
// the endpoint is not an existence/token oracle. A repeat of the same verb
// is idempotent (200); a conflicting verb after a decision is a 409.
func (s *Server) handleApprovalDecision(accept bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		token := approvalToken(r)
		remote := clientIP(r)

		var (
			res approval.DecisionResult
			err error
		)
		if accept {
			res, err = s.deps.Approvals.Accept(id, token, remote)
		} else {
			res, err = s.deps.Approvals.Reject(id, token, remote)
		}
		switch {
		case errors.Is(err, approval.ErrNotFound), errors.Is(err, approval.ErrTokenMismatch):
			// Uniform 404 — no oracle distinguishing unknown id from bad token.
			s.lg.Info("approval decision rejected", "approval_id", id, "reason", "not-found-or-bad-token")
			writeApprovalJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case errors.Is(err, approval.ErrAlreadyDecided):
			s.lg.Info("approval decision conflict", "approval_id", id)
			writeApprovalJSON(w, http.StatusConflict, map[string]any{
				"approval_id": id, "state": string(res.State), "note": "already decided with a different outcome",
			})
		case err != nil:
			s.lg.Error("approval decision failed", "approval_id", id, "error", err.Error())
			writeApprovalJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		default:
			note := "recorded"
			if res.AlreadyDecided {
				note = "already decided"
			}
			s.lg.Info("approval decision recorded", "approval_id", id, "state", string(res.State))
			writeApprovalJSON(w, http.StatusOK, map[string]any{
				"approval_id": id, "state": string(res.State), "note": note,
			})
		}
	}
}

// handleApprovalStatus returns the state of an approval to its owning
// subject only. A foreign subject (or an unknown id) gets a 404 so the
// endpoint does not leak another subject's approval state.
func (s *Server) handleApprovalStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, _ := identity.FromContext(r.Context())
	v, ok := s.deps.Approvals.Get(id)
	if !ok || approval.SubjectKeyOf(v) != subjectKeyOf(user) {
		writeApprovalJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeApprovalJSON(w, http.StatusOK, map[string]any{
		"approval_id": v.ID,
		"state":       string(v.State),
		"expires_at":  v.ExpiresAt,
	})
}

func subjectKeyOf(u *identity.UserCtx) string {
	if u == nil {
		return "\x00anonymous"
	}
	return u.Issuer + "\x00" + u.Subject
}

// approvalToken pulls the capability token from the query string or the
// X-Approval-Token header.
func approvalToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return r.Header.Get("X-Approval-Token")
}

func clientIP(r *http.Request) string {
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return ""
}

func writeApprovalJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
