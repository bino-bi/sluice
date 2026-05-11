// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// ComputeHash returns sha256(prior || "\n" || canonicalJSON(r with hash=""))
// as a lowercase hex string. It does not mutate r.
//
// Callers must use the returned value as the record's `Hash` field before
// writing the line to a sink; that value becomes the `prior_hash` for the
// next record in the chain.
func ComputeHash(prior string, r *Record) (string, error) {
	var buf bytes.Buffer
	buf.Grow(len(prior) + 256)
	buf.WriteString(prior)
	buf.WriteByte('\n')

	// We hash the canonical body excluding `hash` — the record passed in
	// may or may not already have `Hash` populated; CanonicalJSON ignores
	// it either way.
	if err := CanonicalJSON(&buf, r); err != nil {
		return "", fmt.Errorf("audit: compute hash: %w", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

// GenesisPriorHash derives the first chain element's prior hash from the
// configured seed. sha256(seed) binds the chain to a known anchor so a
// replay attacker cannot roll the chain back to an empty state.
func GenesisPriorHash(seed []byte) string {
	sum := sha256.Sum256(seed)
	return hex.EncodeToString(sum[:])
}
