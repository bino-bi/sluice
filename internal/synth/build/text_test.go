// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// sentenceSQL matches one sentence, as profile.sentenceRe.
const sentenceSQL = `'[^.!?]*[^.!?\s][^.!?]*'`

// notesBuild builds crm.orders with the given notes column and returns
// the query connection and the notes report.
func notesBuild(t *testing.T, notes apitypes.SyntheticColumn) (*sql.DB, ColumnReport) {
	t.Helper()
	dir := t.TempDir()
	rep, _ := mustRun(t, opts{spec: ordersSpec(map[string]apitypes.SyntheticColumn{"notes": notes}), dir: dir, source: relationsPath(t)})
	db := relQuery(t, Layout{Dir: dir, Name: "crmsynth"}.Snapshot(rep.BuildID))
	for _, c := range rep.Tables[0].Columns {
		if c.Name == "notes" {
			return db, c
		}
	}
	t.Fatal("no notes in the report")
	return nil, ColumnReport{}
}

// checkSourceWords asserts that every synthetic word occurs at least 20
// times in the source and in at least 5 source rows.
func checkSourceWords(t *testing.T, db *sql.DB) {
	t.Helper()
	const q = `
WITH r AS (SELECT row_number() OVER () AS rn, notes FROM src.crm.orders WHERE notes IS NOT NULL),
w AS (SELECT rn, unnest(regexp_extract_all(lower(notes), '\p{L}+')) AS w FROM r),
ok AS (SELECT w FROM w GROUP BY w HAVING count(*) >= 20 AND count(DISTINCT rn) >= 5),
syn AS (SELECT DISTINCT unnest(regexp_extract_all(lower(notes), '\p{L}+')) AS w FROM syn.crm.orders)
SELECT count(*) FILTER (WHERE w NOT IN (SELECT w FROM ok)), count(*) FILTER (WHERE w = 'zebulon'), count(*) FROM syn`
	var bad, zebulon, words int64
	if err := db.QueryRow(q).Scan(&bad, &zebulon, &words); err != nil {
		t.Fatal(err)
	}
	if bad != 0 || zebulon != 0 || words == 0 {
		t.Errorf("%d synthetic words are not frequent in the source, zebulon %d times, %d words", bad, zebulon, words)
	}
}

// R12.
func TestSourceVocabularyColumn(t *testing.T) {
	db, c := notesBuild(t, apitypes.SyntheticColumn{Generator: apitypes.GenSourceVocabulary})
	checkSourceWords(t, db)
	const kept = `
WITH r AS (SELECT row_number() OVER () AS rn, notes FROM src.crm.orders WHERE notes IS NOT NULL),
w AS (SELECT rn, unnest(regexp_extract_all(lower(notes), '\p{L}+')) AS w FROM r)
SELECT count(*) FROM (SELECT w FROM w WHERE length(w) BETWEEN 2 AND 40 GROUP BY w HAVING count(*) >= 20 AND count(DISTINCT rn) >= 5)`
	if want := scalar[int64](t, db, kept); int64(c.Words) != want || c.SuppressedValues == 0 {
		t.Errorf("report words %d, want %d; suppressed %d", c.Words, want, c.SuppressedValues)
	}

	_, err := run(t, opts{spec: ordersSpec(map[string]apitypes.SyntheticColumn{
		"notes": {Generator: apitypes.GenSourceVocabulary, Args: map[string]any{"minWordCount": 1000000.0}},
	}), source: relationsPath(t)})
	if err == nil || !strings.Contains(err.Error(), "column notes: no word occurs minWordCount times in minGroupSize rows") {
		t.Errorf("no frequent word: err = %v", err)
	}
}

// R13.
func TestSentenceShapeColumn(t *testing.T) {
	db, _ := notesBuild(t, apitypes.SyntheticColumn{Generator: apitypes.GenSentenceShape})
	tv := fmt.Sprintf(`
WITH s AS (SELECT n, count(*) / sum(count(*)) OVER () AS p FROM
             (SELECT len(regexp_extract_all(notes, %[1]s)) AS n FROM src.crm.orders WHERE trim(notes) <> '') GROUP BY n),
     y AS (SELECT n, count(*) / sum(count(*)) OVER () AS p FROM
             (SELECT len(regexp_extract_all(notes, %[1]s)) AS n FROM syn.crm.orders WHERE trim(notes) <> '') GROUP BY n)
SELECT 0.5 * sum(abs(coalesce(s.p, 0) - coalesce(y.p, 0))) FROM s FULL JOIN y ON s.n = y.n`, sentenceSQL)
	if d := scalar[float64](t, db, tv); d > 0.1 {
		t.Errorf("sentence count tv %v", d)
	}
	const shape = `SELECT count(*) FILTER (WHERE NOT regexp_full_match(notes, '\p{Lu}[^.!?]*[.!?]( \p{Lu}[^.!?]*[.!?])*')), count(notes) FROM syn.crm.orders`
	var bad, n int64
	if err := db.QueryRow(shape).Scan(&bad, &n); err != nil {
		t.Fatal(err)
	}
	if bad != 0 || n == 0 {
		t.Errorf("%d of %d notes are not sentences with a capital and a terminator", bad, n)
	}

	db, c := notesBuild(t, apitypes.SyntheticColumn{Generator: apitypes.GenSentenceShape, Args: map[string]any{"vocabulary": "source"}})
	checkSourceWords(t, db)
	if c.Words == 0 {
		t.Error("report words 0 with vocabulary source")
	}
}
