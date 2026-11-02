// SPDX-License-Identifier: AGPL-3.0-or-later

package policy

import (
	"sort"
	"strings"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// ColumnTagger answers which DataClassification tags a column has. The
// synthetic builder uses it for rule matching and strictTags.
type ColumnTagger struct {
	rules []taggedRule
}

type taggedRule struct {
	tag string
	res *compiledResource
	// columns are the rule's column patterns in lower case. Empty means
	// every column of a matching table.
	columns []apitypes.Matcher
}

// NewColumnTagger compiles the classifications of snap. A nil snapshot
// gives a tagger without tags.
func NewColumnTagger(snap *config.Snapshot) (*ColumnTagger, error) {
	t := &ColumnTagger{}
	if snap == nil {
		return t, nil
	}
	index, err := buildTagIndex(snap)
	if err != nil {
		return nil, err
	}
	tags := make([]string, 0, len(index))
	for tag := range index {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		for _, res := range index[tag] {
			tr := taggedRule{tag: tag, res: res}
			// Column names match without regard to case, as in the
			// rewriter (maskColumnMatches).
			for _, m := range res.columns {
				lm, err := apitypes.CompileWildcard(strings.ToLower(m.Pattern()))
				if err != nil {
					return nil, err
				}
				tr.columns = append(tr.columns, lm)
			}
			t.rules = append(t.rules, tr)
		}
	}
	return t, nil
}

// Tags returns the sorted tags of a column. catalog is the source data
// source name.
func (t *ColumnTagger) Tags(catalog, schema, table, column string) []string {
	if t == nil {
		return nil
	}
	ref := parser.TableRef{Catalog: catalog, Schema: schema, Table: table}
	col := strings.ToLower(column)
	var out []string
	for _, r := range t.rules {
		if len(out) > 0 && out[len(out)-1] == r.tag {
			continue
		}
		if !r.res.matches(ref) {
			continue
		}
		if len(r.columns) > 0 && !matchAny(r.columns, col) {
			continue
		}
		out = append(out, r.tag)
	}
	return out
}
