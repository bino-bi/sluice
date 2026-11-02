// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp/syntax"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
	"github.com/bino-bi/sluice/pkg/mask"
)

func text(col Column, g apitypes.SyntheticGenerator) error {
	return fits(col, g, col.Kind() == profile.KindText)
}

const (
	lettersLower = "abcdefghijklmnopqrstuvwxyz"
	lettersUpper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits       = "0123456789"
)

// classChars returns the characters of the classes that passed the
// filter.
func classChars(c profile.CharClasses) []rune {
	var s string
	if c.Upper {
		s += lettersUpper
	}
	if c.Lower {
		s += lettersLower
	}
	if c.Digit {
		s += digits
	}
	if c.Space {
		s += " "
	}
	return []rune(s)
}

// lengthPicker draws a length from a span or the length histogram.
type lengthPicker struct {
	sp   span
	bins []profile.Bin
	cum  []float64
}

func newLengthPicker(sp span, bins []profile.Bin) (*lengthPicker, error) {
	lp := &lengthPicker{sp: sp, bins: bins}
	if sp.source {
		if len(bins) == 0 {
			return nil, errors.New("profile has no length histogram")
		}
		w := make([]float64, len(bins))
		for i, b := range bins {
			w[i] = float64(b.Count)
		}
		lp.cum = cumulative(w)
	}
	return lp, nil
}

func (lp *lengthPicker) next(r *rng.Stream) int {
	if lp.sp.source {
		return int(lp.bins[pickCum(lp.cum, r)].Lo)
	}
	return lp.sp.min + r.IntN(lp.sp.max-lp.sp.min+1)
}

// nominalPicker draws from a nominal histogram.
type nominalPicker struct {
	values []string
	cum    []float64
}

func newNominalPicker(n profile.Nominal) (*nominalPicker, error) {
	if len(n.Values) == 0 {
		return nil, profile.ErrNoGroups
	}
	np := &nominalPicker{}
	w := make([]float64, len(n.Values))
	for i, v := range n.Values {
		np.values = append(np.values, fmt.Sprint(v.V))
		w[i] = float64(v.Count)
	}
	np.cum = cumulative(w)
	return np, nil
}

func (np *nominalPicker) next(r *rng.Stream) string { return np.values[pickCum(np.cum, r)] }

type randomString struct{}

func (randomString) Check(col Column, _ Args) error { return text(col, apitypes.GenRandomString) }

func (randomString) Needs(_ Column, args Args) profile.Needs {
	var n profile.Needs
	if args.span("length").source {
		n.Parts |= profile.PartLengths
	}
	if args.strOr("initial", "random") == "source" {
		n.Parts |= profile.PartInitials
	}
	if args.strOr("charset", "letters") == "source" {
		n.Parts |= profile.PartCharClasses
	}
	if args.strOr("case", "source") == "source" {
		n.Parts |= profile.PartCaseStyles
	}
	return n
}

func (randomString) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := &strGen{r: s.Values, initial: args.strOr("initial", "random"), style: -1}
	var err error
	if g.length, err = newLengthPicker(args.span("length"), p.Lengths); err != nil {
		return nil, err
	}
	switch args.strOr("charset", "letters") {
	case "letters":
		g.chars = []rune(lettersLower)
	case "alnum":
		g.chars = []rune(lettersLower + digits)
	default:
		g.chars = classChars(p.CharClasses)
	}
	switch g.initial {
	case "source":
		if g.initials, err = newNominalPicker(p.Initials); err != nil {
			return nil, err
		}
	case "random":
	default:
		g.fixedInitial = []rune(g.initial)[0]
	}
	switch c := args.strOr("case", "source"); c {
	case "source":
		w := make([]float64, len(p.CaseStyles))
		for i, n := range p.CaseStyles {
			w[i] = float64(n)
		}
		if g.styles = cumulative(w); g.styles[len(g.styles)-1] == 0 {
			return nil, profile.ErrNoGroups
		}
	case "upper":
		g.style = profile.CaseUpper
	case "lower":
		g.style = profile.CaseLower
	case "title":
		g.style = profile.CaseTitle
	}
	return g, nil
}

type strGen struct {
	r            *rng.Stream
	length       *lengthPicker
	chars        []rune
	initial      string
	initials     *nominalPicker
	fixedInitial rune
	// style is a fixed case style, or -1 to pick from styles.
	style  int
	styles []float64
}

func (g *strGen) Next(RowCtx) (any, error) {
	n := g.length.next(g.r)
	if n <= 0 {
		return "", nil
	}
	out := make([]rune, 0, n)
	switch {
	case g.initials != nil:
		out = append(out, []rune(g.initials.next(g.r))...)
	case g.fixedInitial != 0:
		out = append(out, g.fixedInitial)
	}
	out = fillChars(out, n, g.chars, g.r)
	style := g.style
	if style < 0 {
		style = pickCum(g.styles, g.r)
	}
	return applyCase(out, style, g.r), nil
}

func (g *strGen) Pool() int64 { return -1 }

// fillChars appends characters from chars until out has n runes. A space
// is never first or last.
func fillChars(out []rune, n int, chars []rune, r *rng.Stream) []rune {
	inner := []rune(strings.ReplaceAll(string(chars), " ", ""))
	for len(out) < n {
		set := chars
		if len(out) == 0 || len(out) == n-1 || len(inner) == 0 {
			set = inner
		}
		if len(set) == 0 {
			set = []rune(lettersLower)
		}
		out = append(out, set[r.IntN(len(set))])
	}
	return out
}

// applyCase sets the case style. Mixed draws upper or lower per char.
func applyCase(rs []rune, style int, r *rng.Stream) string {
	s := string(rs)
	switch style {
	case profile.CaseUpper:
		return strings.ToUpper(s)
	case profile.CaseLower:
		return strings.ToLower(s)
	case profile.CaseTitle:
		first, size := utf8.DecodeRuneInString(s)
		return string(unicode.ToUpper(first)) + strings.ToLower(s[size:])
	}
	out := make([]rune, len(rs))
	for i, c := range rs {
		if r.Float64() < 0.5 {
			out[i] = unicode.ToUpper(c)
		} else {
			out[i] = unicode.ToLower(c)
		}
	}
	return string(out)
}

type pattern struct{}

func (pattern) Check(col Column, _ Args) error { return text(col, apitypes.GenPattern) }

func (pattern) Needs(_ Column, args Args) profile.Needs {
	return profile.Needs{Parts: profile.PartPatterns, MaxPatterns: args.intOr("maxPatterns", 20)}
}

func (pattern) New(_ Column, p *profile.Column, _ Args, s Streams) (ColumnGen, error) {
	np, err := newNominalPicker(p.Patterns)
	if err != nil {
		return nil, err
	}
	return &patternGen{np: np, r: s.Values}, nil
}

type patternGen struct {
	np *nominalPicker
	r  *rng.Stream
}

func (g *patternGen) Next(RowCtx) (any, error) {
	return fillPattern(g.np.next(g.r), g.r), nil
}

func (g *patternGen) Pool() int64 { return -1 }

// fillPattern replaces A, a and 9 with a random upper-case letter,
// lower-case letter and digit. Other characters stay.
func fillPattern(p string, r *rng.Stream) string {
	var b strings.Builder
	for _, c := range p {
		switch c {
		case 'A':
			b.WriteByte(lettersUpper[r.IntN(26)])
		case 'a':
			b.WriteByte(lettersLower[r.IntN(26)])
		case '9':
			b.WriteByte(digits[r.IntN(10)])
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

type regex struct{}

func (regex) Check(col Column, _ Args) error { return text(col, apitypes.GenRegex) }

func (regex) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (regex) New(_ Column, _ *profile.Column, args Args, s Streams) (ColumnGen, error) {
	// The parsed tree is walked as is: Simplify would expand repeats, and
	// the decode check computes the output cap on this tree.
	re, err := syntax.Parse(args.strOr("pattern", ""), syntax.Perl)
	if err != nil {
		return nil, errors.New("pattern is not a valid regular expression")
	}
	return &regexGen{re: re, r: s.Values}, nil
}

type regexGen struct {
	re *syntax.Regexp
	r  *rng.Stream
}

func (g *regexGen) Next(RowCtx) (any, error) {
	var b strings.Builder
	writeRegex(&b, g.re, g.r)
	return b.String(), nil
}

func (g *regexGen) Pool() int64 { return -1 }

// openRepeat caps *, + and {n,} at n + 8, as apitypes does.
const openRepeat = 8

func writeRegex(b *strings.Builder, re *syntax.Regexp, r *rng.Stream) {
	switch re.Op {
	case syntax.OpLiteral:
		b.WriteString(string(re.Rune))
	case syntax.OpCharClass:
		b.WriteRune(classRune(re.Rune, r))
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		b.WriteRune(rune(0x20 + r.IntN(0x7f-0x20)))
	case syntax.OpCapture:
		writeRegex(b, re.Sub[0], r)
	case syntax.OpConcat:
		for _, sub := range re.Sub {
			writeRegex(b, sub, r)
		}
	case syntax.OpAlternate:
		writeRegex(b, re.Sub[r.IntN(len(re.Sub))], r)
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		lo, hi := 0, 1
		switch re.Op {
		case syntax.OpStar:
			hi = openRepeat
		case syntax.OpPlus:
			lo, hi = 1, 1+openRepeat
		case syntax.OpRepeat:
			lo, hi = re.Min, re.Max
			if hi < 0 {
				hi = lo + openRepeat
			}
		}
		n := lo + r.IntN(hi-lo+1)
		for range n {
			writeRegex(b, re.Sub[0], r)
		}
	}
	// Anchors, word boundaries and empty matches write nothing.
}

// classRune draws from a character class given as rune ranges. When the
// class holds printable ASCII, only those are drawn, so a negated class
// such as [^a] does not give random code points.
func classRune(ranges []rune, r *rng.Stream) rune {
	var ascii []rune
	for i := 0; i+1 < len(ranges); i += 2 {
		for c := max(ranges[i], 0x20); c <= min(ranges[i+1], 0x7e); c++ {
			ascii = append(ascii, c)
		}
	}
	if len(ascii) > 0 {
		return ascii[r.IntN(len(ascii))]
	}
	total := 0
	for i := 0; i+1 < len(ranges); i += 2 {
		total += int(ranges[i+1]-ranges[i]) + 1
	}
	if total == 0 {
		return '?'
	}
	n := r.IntN(total)
	for i := 0; i+1 < len(ranges); i += 2 {
		size := int(ranges[i+1]-ranges[i]) + 1
		if n < size {
			return ranges[i] + rune(n)
		}
		n -= size
	}
	return ranges[0]
}

type fake struct{}

func (fake) Check(col Column, args Args) error {
	if args.strOr("kind", "") == "uuid" && col.Kind() == profile.KindUUID {
		return nil
	}
	return text(col, apitypes.GenFake)
}

func (fake) Needs(_ Column, args Args) profile.Needs {
	if args.boolOr("keepInitial", false) {
		return profile.Needs{Parts: profile.PartInitials}
	}
	return profile.Needs{}
}

func (fake) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := &fakeGen{kind: args.strOr("kind", ""), r: s.Values, lists: map[string][]string{}}
	for _, k := range []string{"first_name", "last_name", "company", "city", "country"} {
		g.lists[k] = mask.FakeWords(k)
	}
	if args.boolOr("keepInitial", false) {
		np, err := newNominalPicker(p.Initials)
		if err != nil {
			return nil, err
		}
		g.initials = np
	}
	return g, nil
}

type fakeGen struct {
	kind      string
	lists     map[string][]string
	r         *rng.Stream
	initials  *nominalPicker
	fallbacks int64
}

func (g *fakeGen) Fallbacks() int64 { return g.fallbacks }

func (g *fakeGen) Pool() int64 {
	switch g.kind {
	case "first_name", "last_name", "company", "city", "country":
		return int64(len(g.lists[g.kind]))
	}
	return -1
}

// word picks a list word. With keepInitial it picks among the words with
// a source initial and counts a fallback when none has it.
func (g *fakeGen) word(list []string, keep bool) string {
	if keep && g.initials != nil {
		init := g.initials.next(g.r)
		var match []string
		for _, w := range list {
			if strings.HasPrefix(strings.ToUpper(w), strings.ToUpper(init)) {
				match = append(match, w)
			}
		}
		if len(match) > 0 {
			return match[g.r.IntN(len(match))]
		}
		g.fallbacks++
	}
	return list[g.r.IntN(len(list))]
}

func (g *fakeGen) Next(RowCtx) (any, error) {
	switch g.kind {
	case "first_name", "last_name", "city", "country":
		return g.word(g.lists[g.kind], true), nil
	case "company":
		return g.word(g.lists["company"], true) + " Inc.", nil
	case "name":
		return g.word(g.lists["first_name"], true) + " " + g.word(g.lists["last_name"], false), nil
	case "email":
		first := g.word(g.lists["first_name"], false)
		last := g.word(g.lists["last_name"], false)
		return strings.ToLower(first) + "." + strings.ToLower(last) + "@example.org", nil
	case "phone":
		return fmt.Sprintf("+1-555-%03d-%04d", g.r.IntN(1000), g.r.IntN(10000)), nil
	case "street":
		n := 1 + g.r.IntN(999)
		name := g.word(g.lists["last_name"], true)
		return fmt.Sprintf("%d %s %s", n, name, streetSuffixes[g.r.IntN(len(streetSuffixes))]), nil
	case "iban":
		return randomIBAN(g.r), nil
	case "uuid":
		return uuidV4(g.r), nil
	}
	return nil, fmt.Errorf("unknown fake kind %q", g.kind)
}

// randomIBAN returns a German IBAN with valid check digits (ISO 13616)
// and 18 random digits.
func randomIBAN(r *rng.Stream) string {
	bban := make([]byte, 18)
	for i := range bban {
		bban[i] = digits[r.IntN(10)]
	}
	// Check digits: 98 - (BBAN + "DE00" with D=13, E=14) mod 97.
	n, _ := new(big.Int).SetString(string(bban)+"131400", 10)
	check := 98 - new(big.Int).Mod(n, big.NewInt(97)).Int64()
	return fmt.Sprintf("DE%02d%s", check, bban)
}

func formatUUID(b []byte) string {
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func uuidV4(r *rng.Stream) string {
	var b [16]byte
	for i := 0; i < 16; i += 8 {
		v := r.Uint64()
		for j := range 8 {
			b[i+j] = byte(v >> (56 - 8*j))
		}
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return formatUUID(b[:])
}

type uuidGen struct{}

func (uuidGen) Check(col Column, _ Args) error {
	k := col.Kind()
	return fits(col, apitypes.GenUUID, k == profile.KindUUID || k == profile.KindText)
}

func (uuidGen) Needs(Column, Args) profile.Needs { return profile.Needs{} }

// v7Epoch is the start of the v7 time field. Values are sorted and
// deterministic; they do not show the build time.
var v7Epoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()

const msPerYear = 365 * 24 * 3600 * 1000

func (uuidGen) New(_ Column, _ *profile.Column, args Args, s Streams) (ColumnGen, error) {
	g := &uuidGenerator{v7: args.intOr("version", 4) == 7, r: s.Values}
	if g.v7 {
		g.base = v7Epoch + int64(s.Values.IntN(msPerYear))
	}
	return g, nil
}

type uuidGenerator struct {
	v7   bool
	base int64
	r    *rng.Stream
}

func (g *uuidGenerator) Pool() int64 { return -1 }

func (g *uuidGenerator) Next(row RowCtx) (any, error) {
	if !g.v7 {
		return uuidV4(g.r), nil
	}
	var b [16]byte
	ms := uint64(g.base + row.Index)
	for i := range 6 {
		b[i] = byte(ms >> (40 - 8*i))
	}
	a, c := g.r.Uint64(), g.r.Uint64()
	b[6] = 0x70 | byte(a>>60)&0x0f
	b[7] = byte(a >> 52)
	b[8] = 0x80 | byte(c>>56)&0x3f
	for i := 9; i < 16; i++ {
		b[i] = byte(c >> (56 - 8*(i-8)))
	}
	return formatUUID(b[:]), nil
}

type prefixKeep struct{}

func (prefixKeep) Check(col Column, _ Args) error { return text(col, apitypes.GenPrefixKeep) }

func (prefixKeep) Needs(_ Column, args Args) profile.Needs {
	return profile.Needs{
		Parts:        profile.PartPrefixes | profile.PartLengths | profile.PartCharClasses,
		PrefixLength: args.intOr("prefixLength", 1),
	}
}

func (prefixKeep) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	np, err := newNominalPicker(p.Prefixes)
	if err != nil {
		return nil, err
	}
	lp, err := newLengthPicker(span{source: true}, p.Lengths)
	if err != nil {
		return nil, err
	}
	return &prefixGen{prefixes: np, length: lp, chars: classChars(p.CharClasses), r: s.Values}, nil
}

type prefixGen struct {
	prefixes *nominalPicker
	length   *lengthPicker
	chars    []rune
	r        *rng.Stream
}

func (g *prefixGen) Pool() int64 { return -1 }

func (g *prefixGen) Next(RowCtx) (any, error) {
	out := []rune(g.prefixes.next(g.r))
	n := max(g.length.next(g.r), len(out))
	return string(fillChars(out, n, g.chars, g.r)), nil
}
