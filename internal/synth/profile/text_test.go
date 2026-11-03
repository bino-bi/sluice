// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// textFixture: src.crm.w has alpha, beta and zeta in 25 rows each,
// gamma 30 times in one row, delta in 10 rows, a one-letter word and a
// number. src.crm.s has 20 rows with two 5-word sentences (. and !), 20
// rows with one 7-word sentence (?), 3 rows with a 1-word and a 2-word
// sentence, one row with five 9-word sentences, a blank row and a NULL.
const textFixture = `
CREATE TABLE src.crm.w AS
SELECT x FROM (
  SELECT 'Alpha beta, x 42 Zeta!' AS x FROM range(25)
  UNION ALL SELECT repeat('gamma ', 30)
  UNION ALL SELECT 'delta' FROM range(10)
  UNION ALL SELECT NULL);
CREATE TABLE src.crm.s AS
SELECT x FROM (
  SELECT 'One two three four five. Six seven eight nine ten!' AS x FROM range(20)
  UNION ALL SELECT 'Eins zwei drei vier five six seven?' FROM range(20)
  UNION ALL SELECT 'Hi. Ho ho.' FROM range(3)
  UNION ALL SELECT repeat('A b c d e f g h i. ', 5)
  UNION ALL SELECT '   '
  UNION ALL SELECT NULL);
`

func textRunner(t *testing.T) *Runner {
	t.Helper()
	r, _ := newRunner(t)
	if _, err := r.Conn.ExecContext(context.Background(), textFixture); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return r
}

func textOf(t *testing.T, r *Runner, table string, needs Needs) (*Column, error) {
	t.Helper()
	ctx := context.Background()
	tb := Table{Catalog: "src", Schema: "crm", Name: table}
	info := ColumnInfo{Name: "x", Type: "VARCHAR"}
	counts, err := r.Base(ctx, tb, []ColumnInfo{info})
	if err != nil {
		t.Fatal(err)
	}
	return r.Column(ctx, tb, info, counts[0], needs, Effective(), jitterStream(0))
}

func TestVocabulary(t *testing.T) {
	r := textRunner(t)
	p, err := textOf(t, r, "w", Needs{Parts: PartVocabulary, MinWordCount: 20, MaxWords: 1000})
	if err != nil {
		t.Fatal(err)
	}
	// gamma has 30 occurrences in one row, delta 10 in 10 rows: both
	// dropped. x is too short, 42 is no word.
	want := Nominal{Values: []Value{{"alpha", 25}, {"beta", 25}, {"zeta", 25}}, Rest: 40}
	if !reflect.DeepEqual(p.Vocabulary, want) || p.Suppressed != 2 {
		t.Errorf("vocabulary %+v, suppressed %d", p.Vocabulary, p.Suppressed)
	}

	p, err = textOf(t, r, "w", Needs{Parts: PartVocabulary, MinWordCount: 20, MaxWords: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Value{{"alpha", 25}, {"beta", 25}}; !reflect.DeepEqual(p.Vocabulary.Values, want) || p.Vocabulary.Rest != 65 {
		t.Errorf("maxWords 2: %+v", p.Vocabulary)
	}

	_, err = textOf(t, r, "w", Needs{Parts: PartVocabulary, MinWordCount: 26, MaxWords: 1000})
	if !errors.Is(err, ErrNoWords) || err.Error() != "column x: no word occurs minWordCount times in minGroupSize rows" {
		t.Errorf("minWordCount 26: err = %v", err)
	}
}

func TestSentenceParts(t *testing.T) {
	r := textRunner(t)
	p, err := textOf(t, r, "s", Needs{Parts: PartSentences | PartSentenceWords | PartTerminators})
	if err != nil {
		t.Fatal(err)
	}
	// The row with five sentences merges into the 2-sentence bin.
	if want := []Bin{{1, 1, 20, 0}, {2, 2, 24, 0}}; !reflect.DeepEqual(p.Sentences, want) {
		t.Errorf("sentences %v", p.Sentences)
	}
	// The five 9-word sentences come from one row: 5 sentences but 1 row,
	// so they merge. The 1-word and 2-word bins have 3 rows each; merged
	// they sum to 6, but the recount finds the same 3 rows, so they merge
	// again into the 5-word bin.
	if want := []Bin{{5, 5, 46, 23}, {7, 7, 25, 21}}; !reflect.DeepEqual(p.SentenceWords, want) {
		t.Errorf("sentence words %v", p.SentenceWords)
	}
	if p.MergedBins != 4 {
		t.Errorf("merged bins %d, want 4", p.MergedBins)
	}
	want := Nominal{Values: []Value{{".", 31}, {"!", 20}, {"?", 20}}}
	if !reflect.DeepEqual(p.Terminators, want) {
		t.Errorf("terminators %+v", p.Terminators)
	}

	if _, err := r.Column(context.Background(), tbl, ColumnInfo{Name: "id", Type: "BIGINT"}, Counts{Rows: 2000, NonNull: 2000},
		Needs{Parts: PartVocabulary}, Effective(), jitterStream(0)); !errors.Is(err, ErrKind) {
		t.Errorf("vocabulary on BIGINT: err = %v, want ErrKind", err)
	}
}

// A sentence after a line break or tab counts only its words.
func TestSentenceWordsLeadingWhitespace(t *testing.T) {
	r := textRunner(t)
	if _, err := r.Conn.ExecContext(context.Background(),
		"CREATE TABLE src.crm.nl AS SELECT 'Hello there.' || chr(10) || 'World here.' || chr(9) || 'Tab one.' AS x FROM range(25)"); err != nil {
		t.Fatal(err)
	}
	p, err := textOf(t, r, "nl", Needs{Parts: PartSentenceWords})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Bin{{2, 2, 75, 25}}; !reflect.DeepEqual(p.SentenceWords, want) {
		t.Errorf("sentence words %v", p.SentenceWords)
	}
}

// A rare terminator is dropped, and none kept gives an empty part.
func TestTerminatorsBelowK(t *testing.T) {
	r := textRunner(t)
	if _, err := r.Conn.ExecContext(context.Background(),
		"CREATE TABLE src.crm.t2 AS SELECT CASE WHEN i < 2 THEN 'Ok?' ELSE 'no end here' END AS x FROM range(30) r(i)"); err != nil {
		t.Fatal(err)
	}
	p, err := textOf(t, r, "t2", Needs{Parts: PartTerminators})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Terminators.Values) != 0 || p.Suppressed != 1 || p.Terminators.Rest != 2 {
		t.Errorf("terminators %+v, suppressed %d", p.Terminators, p.Suppressed)
	}
}
