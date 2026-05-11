// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// VerifyReport summarises a successful chain walk. On failure Verify
// returns an *VerifyError keyed to the first tampered record.
type VerifyReport struct {
	Files    int
	Records  int
	LastHash string
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
func Verify(dir, anchor string) (*VerifyReport, error) {
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
	sort.Strings(files)

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
			computed, herr := ComputeHash(r.PriorHash, &r)
			if herr != nil {
				_ = f.Close()
				return nil, fmt.Errorf("audit: hash %s:%d: %w", name, line, herr)
			}
			if computed != r.Hash {
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
			if errors.Is(scanErr, io.EOF) {
				continue
			}
			return nil, fmt.Errorf("audit: scan %s: %w", name, scanErr)
		}
		_ = f.Close()
	}
	rep.LastHash = prior
	return rep, nil
}
