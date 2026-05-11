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
}
