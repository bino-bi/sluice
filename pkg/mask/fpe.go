// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/capitalone/fpe/ff1"
)

// Alphabet presets. FF1's radix is capped at 36 (big.MaxBase), so the
// built-in alphabets are digits and lowercase letters; mixed-case or
// symbol-rich domains must supply a custom alphabet of at most 36 unique
// characters. Characters outside the alphabet pass through positionally
// (so separators like "-" or "@" are preserved).
const (
	alphabetNumeric      = "0123456789"
	alphabetLower        = "abcdefghijklmnopqrstuvwxyz"
	alphabetAlphanumeric = "0123456789abcdefghijklmnopqrstuvwxyz"
)

const fpeMaxTweakLen = 16

// fpeProvider applies FF1 format-preserving encryption to a value's
// in-alphabet characters. It is a post-query mask: FPE cannot run in SQL.
type fpeProvider struct{}

func newFPEProvider() Provider { return &fpeProvider{} }

// Type returns "fpe".
func (fpeProvider) Type() string { return "fpe" }

// MaskSQL always reports post-query-only.
func (fpeProvider) MaskSQL(_ MaskContext) (string, []Param, error) {
	return "", nil, ErrPostQueryOnly
}

// MaskArrow is not supported; the post-query path is NewRowMask.
func (fpeProvider) MaskArrow(_ MaskArrowContext) error { return ErrSQLOnly }

// ValidateArgs checks the key reference, tweak hex, and alphabet without
// performing I/O (key bytes resolve in NewRowMask).
func (fpeProvider) ValidateArgs(a Args) error {
	if a.KeyRef == "" {
		return fmt.Errorf("%w: keyRef required", ErrInvalidArgs)
	}
	if a.Tweak != "" {
		if _, err := hex.DecodeString(a.Tweak); err != nil {
			return fmt.Errorf("%w: tweak must be hex: %w", ErrInvalidArgs, err)
		}
		if len(a.Tweak)/2 > fpeMaxTweakLen {
			return fmt.Errorf("%w: tweak longer than %d bytes", ErrInvalidArgs, fpeMaxTweakLen)
		}
	}
	if _, err := resolveAlphabet(a); err != nil {
		return err
	}
	return nil
}

// NewRowMask resolves the key and builds an FF1 cipher for this column.
func (fpeProvider) NewRowMask(ctx RowMaskContext) (RowMask, error) {
	if ctx.Keys == nil {
		return nil, fmt.Errorf("%w: no key store configured", ErrInvalidArgs)
	}
	key, err := ctx.Keys.Get(ctx.Ctx, ctx.Args.KeyRef)
	if err != nil {
		return nil, fmt.Errorf("resolve keyRef: %w", err)
	}
	var tweak []byte
	if ctx.Args.Tweak != "" {
		tweak, _ = hex.DecodeString(ctx.Args.Tweak)
	}
	alphabet, err := resolveAlphabet(ctx.Args)
	if err != nil {
		return nil, err
	}
	cipher, err := ff1.NewCipher(len(alphabet), fpeMaxTweakLen, key, tweak)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidArgs, err)
	}
	index := make(map[rune]int, len(alphabet))
	for i, r := range alphabet {
		index[r] = i
	}
	return &fpeRowMask{cipher: cipher, alphabet: []rune(alphabet), index: index}, nil
}

func resolveAlphabet(a Args) (string, error) {
	if a.CustomAlphabet != "" {
		if err := checkAlphabet(a.CustomAlphabet); err != nil {
			return "", err
		}
		return a.CustomAlphabet, nil
	}
	switch a.Alphabet {
	case "", "numeric":
		return alphabetNumeric, nil
	case "lower":
		return alphabetLower, nil
	case "alphanumeric":
		return alphabetAlphanumeric, nil
	default:
		return "", fmt.Errorf("%w: unknown alphabet %q (use numeric, lower, alphanumeric, or customAlphabet)", ErrInvalidArgs, a.Alphabet)
	}
}

func checkAlphabet(alpha string) error {
	runes := []rune(alpha)
	if len(runes) < 2 || len(runes) > 36 {
		return fmt.Errorf("%w: customAlphabet must have 2-36 characters (FF1 radix limit)", ErrInvalidArgs)
	}
	seen := map[rune]struct{}{}
	for _, r := range runes {
		if _, dup := seen[r]; dup {
			return fmt.Errorf("%w: customAlphabet has duplicate character %q", ErrInvalidArgs, r)
		}
		seen[r] = struct{}{}
	}
	return nil
}

// radixDigits are FF1's native digit characters, indexed by value.
const radixDigits = "0123456789abcdefghijklmnopqrstuvwxyz"

type fpeRowMask struct {
	cipher   ff1.Cipher
	alphabet []rune
	index    map[rune]int
}

// Mask FPE-encrypts the in-alphabet characters of the value, preserving
// position of any pass-through characters. A value with fewer in-alphabet
// characters than FF1's domain minimum returns an error, aborting the
// stream fail-closed rather than leaking the plaintext.
func (m *fpeRowMask) Mask(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	s, ok := value.(string)
	if !ok {
		s = fmt.Sprintf("%v", value)
	}
	// Extract in-alphabet characters, transcoded to FF1 radix digits.
	var payload strings.Builder
	positions := make([]int, 0, len(s))
	runes := []rune(s)
	for i, r := range runes {
		if idx, ok := m.index[r]; ok {
			payload.WriteByte(radixDigits[idx])
			positions = append(positions, i)
		}
	}
	if payload.Len() == 0 {
		return s, nil
	}
	enc, err := m.cipher.Encrypt(payload.String())
	if err != nil {
		return nil, fmt.Errorf("fpe encrypt: %w", err)
	}
	for k, ch := range []byte(enc) {
		runes[positions[k]] = m.alphabet[radixIndex(ch)]
	}
	return string(runes), nil
}

func radixIndex(ch byte) int {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0')
	case ch >= 'a' && ch <= 'z':
		return int(ch-'a') + 10
	default:
		return 0
	}
}
