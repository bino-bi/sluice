// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/internal/synth/rng"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

type loremText struct{}

func (loremText) Check(col Column, _ Args) error { return text(col, apitypes.GenLoremText) }

func (loremText) Needs(_ Column, args Args) profile.Needs {
	if args.span("words").source {
		return profile.Needs{Parts: profile.PartWordCounts}
	}
	return profile.Needs{}
}

func (loremText) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	lp, err := newLengthPicker(args.span("words"), p.WordCounts)
	if err != nil {
		return nil, err
	}
	return &loremGen{words: lp, vocab: vocabulary(args.strOr("vocabulary", "lorem")), r: s.Values}, nil
}

type loremGen struct {
	words *lengthPicker
	vocab []string
	r     *rng.Stream
}

func (g *loremGen) Pool() int64 { return -1 }

func (g *loremGen) Next(RowCtx) (any, error) {
	n := max(g.words.next(g.r), 1)
	ws := make([]string, n)
	for i := range ws {
		ws[i] = g.vocab[g.r.IntN(len(g.vocab))]
	}
	s := strings.Join(ws, " ")
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:] + ".", nil
}

type randomText struct{}

func (randomText) Check(col Column, _ Args) error { return text(col, apitypes.GenRandomText) }

func (randomText) Needs(_ Column, args Args) profile.Needs {
	if args.span("length").source {
		return profile.Needs{Parts: profile.PartLengths}
	}
	return profile.Needs{}
}

func (randomText) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	lp, err := newLengthPicker(args.span("length"), p.Lengths)
	if err != nil {
		return nil, err
	}
	return &randomTextGen{length: lp, vocab: vocabulary(args.strOr("vocabulary", "lorem")), r: s.Values}, nil
}

type randomTextGen struct {
	length *lengthPicker
	vocab  []string
	r      *rng.Stream
}

func (g *randomTextGen) Pool() int64 { return -1 }

// Next adds words until the text has at least L characters, cuts it at
// L and trims spaces at the end.
func (g *randomTextGen) Next(RowCtx) (any, error) {
	n := g.length.next(g.r)
	var out []rune
	for len(out) < n {
		if len(out) > 0 {
			out = append(out, ' ')
		}
		out = append(out, []rune(g.vocab[g.r.IntN(len(g.vocab))])...)
	}
	if len(out) > n {
		out = out[:n]
	}
	return strings.TrimRight(string(out), " "), nil
}

type redact struct{}

func (redact) Check(col Column, _ Args) error { return text(col, apitypes.GenRedact) }

func (redact) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (redact) New(_ Column, _ *profile.Column, args Args, _ Streams) (ColumnGen, error) {
	return fixed{args.strOr("value", "[redacted]")}, nil
}

type null struct{}

func (null) Check(Column, Args) error { return nil }

func (null) Needs(Column, Args) profile.Needs { return profile.Needs{} }

func (null) New(Column, *profile.Column, Args, Streams) (ColumnGen, error) { return nullGen{}, nil }

type nullGen struct{}

func (nullGen) Next(RowCtx) (any, error) { return nil, nil }
func (nullGen) Pool() int64              { return -1 }
