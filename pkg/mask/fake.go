// SPDX-License-Identifier: Apache-2.0

package mask

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// fakeType names a deterministic generator.
const (
	fakeName      = "name"
	fakeFirstName = "first_name"
	fakeLastName  = "last_name"
	fakeEmail     = "email"
	fakePhone     = "phone"
	fakeCompany   = "company"
	fakeCity      = "city"
	fakeCountry   = "country"
	fakeUUID      = "uuid"
)

// fakeProvider replaces a value with a plausible fake of the configured
// type. The choice is a keyed hash of the input so the same value always
// maps to the same fake, keeping joins and grouping consistent. Post-query
// only.
type fakeProvider struct{}

func newFakeProvider() Provider { return &fakeProvider{} }

// Type returns "fake".
func (fakeProvider) Type() string { return "fake" }

// MaskSQL always reports post-query-only.
func (fakeProvider) MaskSQL(_ MaskContext) (string, []Param, error) {
	return "", nil, ErrPostQueryOnly
}

// MaskArrow is not supported; the post-query path is NewRowMask.
func (fakeProvider) MaskArrow(_ MaskArrowContext) error { return ErrSQLOnly }

// ValidateArgs checks the fake type against the supported set.
func (fakeProvider) ValidateArgs(a Args) error {
	switch a.FakeType {
	case fakeName, fakeFirstName, fakeLastName, fakeEmail, fakePhone,
		fakeCompany, fakeCity, fakeCountry, fakeUUID:
		return nil
	case "":
		return fmt.Errorf("%w: fakeType required", ErrInvalidArgs)
	default:
		return fmt.Errorf("%w: unknown fakeType %q", ErrInvalidArgs, a.FakeType)
	}
}

// NewRowMask resolves the seed and returns a masker.
func (fakeProvider) NewRowMask(ctx RowMaskContext) (RowMask, error) {
	seed := []byte(ctx.Args.Seed)
	if ctx.Args.Seed != "" && ctx.Salts != nil && looksLikeSecretRef(ctx.Args.Seed) {
		b, err := ctx.Salts.Get(ctx.Ctx, ctx.Args.Seed)
		if err != nil {
			return nil, fmt.Errorf("resolve fake seed: %w", err)
		}
		seed = b
	}
	return &fakeRowMask{seed: seed, kind: ctx.Args.FakeType}, nil
}

type fakeRowMask struct {
	seed []byte
	kind string
}

// Mask returns a deterministic fake for the value's keyed hash.
func (m *fakeRowMask) Mask(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	h := hmac.New(sha256.New, m.seed)
	_, _ = fmt.Fprintf(h, "%s\x00%v", m.kind, value)
	sum := h.Sum(nil)
	n := binary.BigEndian.Uint64(sum[:8])

	switch m.kind {
	case fakeFirstName:
		return pick(fakeFirstNames, n), nil
	case fakeLastName:
		return pick(fakeLastNames, n), nil
	case fakeName:
		return pick(fakeFirstNames, n) + " " + pick(fakeLastNames, n>>16), nil
	case fakeCompany:
		return pick(fakeCompanies, n) + " Inc.", nil
	case fakeCity:
		return pick(fakeCities, n), nil
	case fakeCountry:
		return pick(fakeCountries, n), nil
	case fakeEmail:
		return fmt.Sprintf("%s.%s@example.com",
			toLowerASCII(pick(fakeFirstNames, n)), toLowerASCII(pick(fakeLastNames, n>>16))), nil
	case fakePhone:
		return fmt.Sprintf("+1-555-%03d-%04d", n%1000, (n>>10)%10000), nil
	case fakeUUID:
		return fakeUUIDFromHash(sum), nil
	default:
		return nil, fmt.Errorf("%w: unknown fakeType %q", ErrInvalidArgs, m.kind)
	}
}

func pick(list []string, n uint64) string {
	return list[n%uint64(len(list))]
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// fakeUUIDFromHash renders a v4-shaped UUID from hash bytes (deterministic,
// not a real random UUID — the version/variant nibbles are set so it
// validates as v4).
func fakeUUIDFromHash(sum []byte) string {
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
