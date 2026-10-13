// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

// StmtKind is the top-level statement type.
type StmtKind string

// Statement kinds. Only StmtSelect and StmtExplain are ever permitted at
// the transport boundary; everything else is rejected by the rewriter even
// if a policy would allow it.
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

// IsReadOnly reports whether kind is permitted through the gateway. SET
// and SHOW are not: they name no table for a policy to match, a setting
// stays on the pooled connection for later requests of any subject, and
// SHOW lists tables a deny policy covers.
func (k StmtKind) IsReadOnly() bool {
	switch k {
	case StmtSelect, StmtExplain:
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
	// AccessesAllColumns is set when the statement reads columns it does
	// not name: a star, a whole-row reference to a FROM item, a column
	// alias list on a table, or a NATURAL join. A columnsAccessed trigger
	// then cannot tell which columns are read.
	AccessesAllColumns bool
	// UnreadPredicateColumns lists the columns used in a condition that
	// Comparisons does not describe: inside a function, under NOT or CASE,
	// compared with another column, in a USING join. A predicate trigger
	// on such a column cannot tell which value is compared.
	UnreadPredicateColumns []string

	// Functions lists every function call in the statement, at any depth.
	// Consumed by CheckFunctions; policy and the rewriter key on table
	// references and cannot see what a function reads.
	Functions []FuncRef
	// UnsupportedFrom names FROM items that are not a table, subquery,
	// join or function (TABLESAMPLE, XMLTABLE, ...). No rewriter pass
	// applies row filters or masks to them.
	UnsupportedFrom []string
	// HasParams is set when the statement has a $n parameter reference at
	// any depth. The rewriter binds its own values as $1, $2, ...
	HasParams bool
}

// FuncRef is one function call found in a statement.
type FuncRef struct {
	// Name is the last segment of the function name, lowercased.
	Name string
	// Qualifier is the lowercased, dot-joined prefix ("" when unqualified).
	Qualifier string
	// InFrom is true when the call is a FROM item (a table function).
	InFrom bool
	// DynamicArgs is set for FROM calls whose arguments contain a column
	// reference or a subquery.
	DynamicArgs bool
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
