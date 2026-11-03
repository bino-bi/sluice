// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestTextNeeds(t *testing.T) {
	c := col("notes", "VARCHAR")
	sentences := profile.PartSentences | profile.PartSentenceWords | profile.PartTerminators
	for _, tc := range []struct {
		g    Generator
		args Args
		want profile.Needs
	}{
		{sourceVocabulary{}, nil, profile.Needs{Parts: profile.PartVocabulary | profile.PartWordCounts, MinWordCount: 20, MaxWords: 1000}},
		{sourceVocabulary{}, Args{"words": 3.0, "minWordCount": 7.0, "maxWords": 9.0}, profile.Needs{Parts: profile.PartVocabulary, MinWordCount: 7, MaxWords: 9}},
		{sentenceShape{}, nil, profile.Needs{Parts: sentences}},
		{sentenceShape{}, Args{"vocabulary": "source"}, profile.Needs{Parts: sentences | profile.PartVocabulary, MinWordCount: 20, MaxWords: 1000}},
	} {
		if got := tc.g.Needs(c, tc.args); got != tc.want {
			t.Errorf("%T %v: needs %+v, want %+v", tc.g, tc.args, got, tc.want)
		}
	}
}

func TestSourceVocabularyWeights(t *testing.T) {
	out := draw(t, genCase{"v", apitypes.GenSourceVocabulary, col("notes", "VARCHAR"), Args{"words": 5.0}, textProfile()}, 400)
	counts := map[string]int{}
	for _, v := range out {
		s := v.(string)
		if !strings.HasSuffix(s, ".") {
			t.Fatalf("value %q has no final dot", s)
		}
		for _, w := range strings.Fields(strings.ToLower(strings.TrimSuffix(s, "."))) {
			counts[w]++
		}
	}
	if len(counts) != 3 || counts["invoice"] <= counts["delivery"] || counts["delivery"] <= counts["paid"] {
		t.Errorf("word counts %v, want the weights 90, 60, 20", counts)
	}
}

func TestSentenceShapeEnds(t *testing.T) {
	p := textProfile()
	p.Terminators = profile.Nominal{}
	out := draw(t, genCase{"s", apitypes.GenSentenceShape, col("notes", "VARCHAR"), nil, p}, 200)
	re := regexp.MustCompile(`^[A-Z][a-z]*( [a-z]+)*\.( [A-Z][a-z]*( [a-z]+)*\.)*$`)
	for _, v := range out {
		if !re.MatchString(v.(string)) {
			t.Fatalf("value %q is not sentences ending with a dot", v)
		}
	}

	g := sentenceShape{}
	if _, err := g.New(col("notes", "VARCHAR"), &profile.Column{}, nil, streams("notes")); err == nil {
		t.Error("New without sentence bins: want an error")
	}
	if err := g.Check(col("n", "BIGINT"), nil); err == nil {
		t.Error("Check on BIGINT: want an error")
	}
}
