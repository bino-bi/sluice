// SPDX-License-Identifier: AGPL-3.0-or-later

package budget

import "strings"

// key joins subject + issuer into a counter key. The separator is a NUL so
// it cannot collide with a subject or issuer value.
func key(subject, issuer string) string { return subject + "\x00" + issuer }

func splitKey(k string) (subject, issuer string) {
	if i := strings.IndexByte(k, 0); i >= 0 {
		return k[:i], k[i+1:]
	}
	return k, ""
}

func subjectOf(k string) string {
	s, _ := splitKey(k)
	return s
}
