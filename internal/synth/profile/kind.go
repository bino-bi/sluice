// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"fmt"
	"math"
	"strings"
)

// Kind groups DuckDB column types by how they are profiled.
type Kind int

// Column kinds.
const (
	KindOther Kind = iota
	KindInteger
	KindFloat // FLOAT, DOUBLE, DECIMAL
	KindBool
	KindText // VARCHAR
	KindEnum
	KindDate
	KindTimestamp // every TIMESTAMP variant, also WITH TIME ZONE
	KindTime      // TIME without time zone
	KindUUID
)

// KindOf maps an information_schema data_type string to its kind.
func KindOf(dataType string) Kind {
	t := strings.ToUpper(strings.TrimSpace(dataType))
	switch t {
	case "TINYINT", "SMALLINT", "INTEGER", "BIGINT", "HUGEINT",
		"UTINYINT", "USMALLINT", "UINTEGER", "UBIGINT", "UHUGEINT":
		return KindInteger
	case "FLOAT", "DOUBLE":
		return KindFloat
	case "BOOLEAN":
		return KindBool
	case "VARCHAR":
		return KindText
	case "DATE":
		return KindDate
	case "TIMESTAMP", "TIMESTAMP_S", "TIMESTAMP_MS", "TIMESTAMP_NS", "TIMESTAMP WITH TIME ZONE":
		return KindTimestamp
	case "TIME":
		return KindTime
	case "UUID":
		return KindUUID
	}
	switch {
	case strings.HasPrefix(t, "DECIMAL("):
		return KindFloat
	case strings.HasPrefix(t, "ENUM("):
		return KindEnum
	}
	return KindOther
}

// Numeric reports whether the kind has a number axis: the value for
// numbers, days since 1970-01-01 for DATE, seconds since the epoch for
// TIMESTAMP and seconds of the day for TIME.
func (k Kind) Numeric() bool {
	switch k {
	case KindInteger, KindFloat, KindDate, KindTimestamp, KindTime:
		return true
	}
	return false
}

// axis returns the SQL expression of the number axis of the quoted
// column x, or "" when the kind has none. Every number goes through
// DOUBLE: quantile_cont on DECIMAL returns DECIMAL lists, and HUGEINT
// and UBIGINT do not fit int64.
func (k Kind) axis(x string) string {
	switch k {
	case KindInteger, KindFloat:
		return "CAST(" + x + " AS DOUBLE)"
	case KindDate:
		// DATE 'infinity' minus a date is a large finite number, so
		// isfinite on the axis would not drop it.
		return "CASE WHEN isfinite(" + x + ") THEN CAST(" + x + " - DATE '1970-01-01' AS DOUBLE) END"
	case KindTimestamp, KindTime:
		return "epoch(" + x + ")"
	}
	return ""
}

// defaultUnit is the smallest step of a value of dataType on the number
// axis, or 0 for continuous types. DECIMAL(p,s) steps by 10^-s, because
// the snapshot casts back to the type and a smaller jitter move would
// round back to the source bound.
func defaultUnit(dataType string) float64 {
	switch KindOf(dataType) {
	case KindInteger, KindDate:
		return 1
	case KindFloat:
		var prec, scale int
		if _, err := fmt.Sscanf(strings.ToUpper(strings.TrimSpace(dataType)), "DECIMAL(%d,%d)", &prec, &scale); err == nil {
			return math.Pow10(-scale)
		}
	}
	return 0
}
