// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

// StmtKind is the top-level statement type.
type StmtKind string

// Statement kinds. Only StmtSelect, StmtExplain, StmtSet, StmtShow, and
// StmtPragma are ever permitted at the transport boundary; everything else
// is rejected by the rewriter even if a policy would allow it.
const (
	StmtSelect      StmtKind = "SELECT"
	StmtExplain     StmtKind = "EXPLAIN"
	StmtSet         StmtKind = "SET"
	StmtShow        StmtKind = "SHOW"
	StmtPragma      StmtKind = "PRAGMA"
	StmtInsert      StmtKind = "INSERT"
	StmtUpdate      StmtKind = "UPDATE"
	StmtDelete      StmtKind = "DELETE"
	StmtDDL         StmtKind = "DDL"
	StmtCopy        StmtKind = "COPY"
	StmtAttach      StmtKind = "ATTACH"
	StmtLoad        StmtKind = "LOAD"
	StmtInstall     StmtKind = "INSTALL"
	StmtUnsupported StmtKind = "UNSUPPORTED"
)

// IsReadOnly reports whether kind is permitted through the gateway.
func (k StmtKind) IsReadOnly() bool {
	switch k {
	case StmtSelect, StmtExplain, StmtSet, StmtShow, StmtPragma:
		return true
	default:
		return false
	}
}

// QueryShape summarises the structural features of a query used by
// QueryRejectPolicy rules (concept §3.6) and by the rewriter's
// SELECT-* expansion.
type QueryShape struct {
	HasSelectStar  bool
	IsAggregate    bool
	HasCTE         bool
	HasUnion       bool
	HasLimit       bool
	LimitValue     int64
	HasOrderBy     bool
	HasWhere       bool
	WhereColumns   []string
	GroupByColumns []string
	Joins          int
	Catalogs       []string
	IsRecursiveCTE bool

	// AccessedColumns lists every column referenced anywhere in the query
	// — target list, WHERE, HAVING, GROUP BY, ORDER BY, and JOIN quals,
	// recursing into subqueries, CTEs, and set-operation arms. Used by
	// ApprovalPolicy triggers; a deep walk means a column buried in a
	// subquery still triggers approval (fail-closed, more matches never
	// fewer). Names are dotted as written ("email" or "u.email").
	AccessedColumns []string
	// Comparisons lists (column, op, literal) facts from WHERE/HAVING/JOIN
	// quals, same deep walk. Used by ApprovalPolicy "field compared to a
	// value" triggers.
	Comparisons []Comparison
}

// Comparison is a (column, operator, literal-value) fact extracted from a
// predicate. Op is normalised ("=", "!=", "<", "<=", ">", ">=", "like",
// "ilike", "in", "isnull"); operand order is normalised so the column is
// on the left. Value is the literal rendered as a string ("42", "de",
// "true"); it is "" for the "isnull" op.
type Comparison struct {
	Column string
	Op     string
	Value  string
}
