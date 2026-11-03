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

// vocabNeeds is the vocabulary part of sourceVocabulary and of
// sentenceShape with vocabulary source.
func vocabNeeds(args Args) profile.Needs {
	return profile.Needs{
		Parts:        profile.PartVocabulary,
		MinWordCount: args.intOr("minWordCount", 20),
		MaxWords:     args.intOr("maxWords", 1000),
	}
}

type sourceVocabulary struct{}

func (sourceVocabulary) Check(col Column, _ Args) error {
	return text(col, apitypes.GenSourceVocabulary)
}

func (sourceVocabulary) Needs(_ Column, args Args) profile.Needs {
	n := vocabNeeds(args)
	if args.span("words").source {
		n.Parts |= profile.PartWordCounts
	}
	return n
}

func (sourceVocabulary) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	lp, err := newLengthPicker(args.span("words"), p.WordCounts)
	if err != nil {
		return nil, err
	}
	words, err := newNominalPicker(p.Vocabulary)
	if err != nil {
		return nil, err
	}
	return &vocabGen{words: lp, vocab: words, r: s.Values}, nil
}

// vocabGen draws words by their source weight, so the frequency shape
// of the words stays.
type vocabGen struct {
	words *lengthPicker
	vocab *nominalPicker
	r     *rng.Stream
}

func (g *vocabGen) Pool() int64 { return -1 }

func (g *vocabGen) Next(RowCtx) (any, error) {
	ws := make([]string, max(g.words.next(g.r), 1))
	for i := range ws {
		ws[i] = g.vocab.next(g.r)
	}
	return capitalize(strings.Join(ws, " ")) + ".", nil
}

type sentenceShape struct{}

func (sentenceShape) Check(col Column, _ Args) error { return text(col, apitypes.GenSentenceShape) }

func (sentenceShape) Needs(_ Column, args Args) profile.Needs {
	n := profile.Needs{}
	if args.strOr("vocabulary", "lorem") == "source" {
		n = vocabNeeds(args)
	}
	n.Parts |= profile.PartSentences | profile.PartSentenceWords | profile.PartTerminators
	return n
}

func (sentenceShape) New(_ Column, p *profile.Column, args Args, s Streams) (ColumnGen, error) {
	sentences, err := newLengthPicker(span{source: true}, p.Sentences)
	if err != nil {
		return nil, err
	}
	words, err := newLengthPicker(span{source: true}, p.SentenceWords)
	if err != nil {
		return nil, err
	}
	g := &sentenceGen{sentences: sentences, words: words, r: s.Values}
	if name := args.strOr("vocabulary", "lorem"); name == "source" {
		if g.source, err = newNominalPicker(p.Vocabulary); err != nil {
			return nil, err
		}
	} else {
		g.list = vocabulary(name)
	}
	// No kept terminator ends every sentence with ".", which is no source
	// value.
	if len(p.Terminators.Values) > 0 {
		if g.ends, err = newNominalPicker(p.Terminators); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// sentenceGen keeps the sentence count, the words per sentence and the
// terminators of the source. Words come uniform from a list vocabulary
// or by weight from the source vocabulary.
type sentenceGen struct {
	sentences, words *lengthPicker
	list             []string
	source           *nominalPicker
	ends             *nominalPicker
	r                *rng.Stream
}

func (g *sentenceGen) Pool() int64 { return -1 }

func (g *sentenceGen) Next(RowCtx) (any, error) {
	out := make([]string, max(g.sentences.next(g.r), 1))
	for i := range out {
		ws := make([]string, max(g.words.next(g.r), 1))
		for j := range ws {
			if g.source != nil {
				ws[j] = g.source.next(g.r)
			} else {
				ws[j] = g.list[g.r.IntN(len(g.list))]
			}
		}
		end := "."
		if g.ends != nil {
			end = g.ends.next(g.r)
		}
		out[i] = capitalize(strings.Join(ws, " ")) + end
	}
	return strings.Join(out, " "), nil
}

// capitalize upper-cases the first letter of s.
func capitalize(s string) string {
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:]
}
