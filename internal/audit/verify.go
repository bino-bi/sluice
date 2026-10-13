// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bufio"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VerifyReport summarises a successful chain walk. On failure Verify
// returns an *VerifyError keyed to the first tampered record.
type VerifyReport struct {
	Files    int
	Records  int
	LastHash string
	// Keyed counts the records with a keyed hash. 0 means a plain chain.
	Keyed int
}

// VerifyError describes a broken link. File and Line pinpoint where the
// chain failed.
type VerifyError struct {
	File string
	Line int
	Msg  string
}

// Error satisfies the error interface.
func (e *VerifyError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
}

// Is reports whether the target is ErrChainBroken, enabling the standard
// errors.Is idiom.
func (e *VerifyError) Is(target error) bool {
	return target == ErrChainBroken
}

// Verify walks the audit directory in filename order, recomputing each
// record's hash and carrying the prior hash across file boundaries. It
// returns an *VerifyError at the first mismatch. Callers wrap verification
// behind `sluice audit verify <dir>`.
//
// An optional anchor lets callers pin the genesis event's prior_hash
// (sha256(seed) in the common case). Pass "" to accept whatever the first
// record reports as its prior_hash.
//
// key is the chain key for records with hash_alg hmac-sha256. A keyed
// record without a key gives ErrKeyRequired. A plain record after a keyed
// one breaks the chain: a writer without the key would add such records.
// A directory without any record is a broken chain too, an emptied
// directory must not verify.
func Verify(dir, anchor string, key []byte) (*VerifyReport, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("audit: read dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		files = append(files, name)
	}
	sortAuditNames(files)

	rep := &VerifyReport{Files: len(files)}
	prior := ""
	checkedFirst := false

	for _, name := range files {
		path := filepath.Join(dir, name)
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil, fmt.Errorf("audit: open %s: %w", path, openErr)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			raw := sc.Bytes()
			if len(raw) == 0 {
				continue
			}
			var r Record
			if jerr := json.Unmarshal(raw, &r); jerr != nil {
				_ = f.Close()
				return nil, &VerifyError{File: name, Line: line, Msg: fmt.Sprintf("invalid JSON: %v", jerr)}
			}
			if !checkedFirst {
				checkedFirst = true
				if anchor != "" && r.PriorHash != anchor {
					_ = f.Close()
					return nil, &VerifyError{
						File: name, Line: line,
						Msg: fmt.Sprintf("prior_hash %q does not match anchor %q", r.PriorHash, anchor),
					}
				}
				prior = r.PriorHash
			}
			if r.PriorHash != prior {
				_ = f.Close()
				return nil, &VerifyError{
					File: name, Line: line,
					Msg: fmt.Sprintf("prior_hash %q does not chain; expected %q", r.PriorHash, prior),
				}
			}
			switch {
			case r.HashAlg == "" && rep.Keyed > 0:
				_ = f.Close()
				return nil, &VerifyError{File: name, Line: line, Msg: "record without a key after a keyed record"}
			case r.HashAlg == HashAlgHMAC && len(key) == 0:
				_ = f.Close()
				return nil, fmt.Errorf("%w: %s:%d", ErrKeyRequired, name, line)
			case r.HashAlg != "" && r.HashAlg != HashAlgHMAC:
				_ = f.Close()
				return nil, &VerifyError{File: name, Line: line, Msg: fmt.Sprintf("unknown hash_alg %q", r.HashAlg)}
			}
			computed, herr := chainHash(key, r.PriorHash, &r)
			if herr != nil {
				_ = f.Close()
				return nil, fmt.Errorf("audit: hash %s:%d: %w", name, line, herr)
			}
			if r.HashAlg != "" {
				rep.Keyed++
			}
			if !hmac.Equal([]byte(computed), []byte(r.Hash)) {
				_ = f.Close()
				return nil, &VerifyError{
					File: name, Line: line,
					Msg: fmt.Sprintf("hash mismatch: stored=%s recomputed=%s", r.Hash, computed),
				}
			}
			prior = r.Hash
			rep.Records++
		}
		if scanErr := sc.Err(); scanErr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("audit: scan %s: %w", name, scanErr)
		}
		_ = f.Close()
	}
	if rep.Records == 0 {
		return nil, &VerifyError{File: dir, Msg: "no audit record"}
	}
	rep.LastHash = prior
	return rep, nil
}
