// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bytes"
	"crypto/hmac"
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
	in, err := chainInput(prior, r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(in)
	return hex.EncodeToString(sum[:]), nil
}

// HashAlgHMAC is the hash_alg of a record whose hash is keyed.
const HashAlgHMAC = "hmac-sha256"

// MinChainKeyBytes is the shortest key the keyed chain accepts.
const MinChainKeyBytes = 32

// ComputeKeyedHash returns HMAC-SHA256(key, prior || "\n" ||
// canonicalJSON(r with hash="")) as a lowercase hex string. Without the
// key nobody can compute the hash of a changed record again.
func ComputeKeyedHash(key []byte, prior string, r *Record) (string, error) {
	in, err := chainInput(prior, r)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(in)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// chainInput is the byte string both hashes cover.
func chainInput(prior string, r *Record) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(prior) + 256)
	buf.WriteString(prior)
	buf.WriteByte('\n')

	// We hash the canonical body excluding `hash` — the record passed in
	// may or may not already have `Hash` populated; CanonicalJSON ignores
	// it either way.
	if err := CanonicalJSON(&buf, r); err != nil {
		return nil, fmt.Errorf("audit: compute hash: %w", err)
	}
	return buf.Bytes(), nil
}

// chainHash computes the hash of r the way its hash_alg says.
func chainHash(key []byte, prior string, r *Record) (string, error) {
	switch r.HashAlg {
	case "":
		return ComputeHash(prior, r)
	case HashAlgHMAC:
		return ComputeKeyedHash(key, prior, r)
	default:
		return "", fmt.Errorf("audit: unknown hash_alg %q", r.HashAlg)
	}
}

// GenesisPriorHash derives the first chain element's prior hash from the
// configured seed. sha256(seed) binds the chain to a known anchor so a
// replay attacker cannot roll the chain back to an empty state.
func GenesisPriorHash(seed []byte) string {
	sum := sha256.Sum256(seed)
	return hex.EncodeToString(sum[:])
}
