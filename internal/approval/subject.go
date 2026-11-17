// SPDX-License-Identifier: AGPL-3.0-or-later

package approval

import "github.com/bino-bi/sluice/internal/identity"

// UserSubjectKey is the subject key of a caller, in the form SubjectKeyOf
// gives for a request. A nil user is the anonymous subject.
func UserSubjectKey(u *identity.UserCtx) string {
	if u == nil {
		return "\x00anonymous"
	}
	return u.Issuer + "\x00" + u.Subject
}
