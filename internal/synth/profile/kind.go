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

// DisplayType returns dataType with every ENUM label list cut to the bare
// word ENUM. The list holds rare labels too, so it must not reach a
// report, a log or an error text (plan 1.8, 1.14).
func DisplayType(dataType string) string {
	var b strings.Builder
	for i := 0; i < len(dataType); {
		if !strings.HasPrefix(strings.ToUpper(dataType[i:]), "ENUM(") {
			b.WriteByte(dataType[i])
			i++
			continue
		}
		b.WriteString("ENUM")
		i += len("ENUM(")
		for quoted := false; i < len(dataType); i++ {
			if c := dataType[i]; c == '\'' {
				quoted = !quoted
			} else if c == ')' && !quoted {
				i++
				break
			}
		}
	}
	return b.String()
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

// offsetAxis returns the SQL expression of x - f on the number axis of
// kind k: days for DATE, seconds for TIMESTAMP. Rows with a NULL or
// infinite side give NULL; DATE 'infinity' minus a date would be finite.
// It is "" for other kinds.
func (k Kind) offsetAxis(x, f string) string {
	finite := "isfinite(" + x + ") AND isfinite(" + f + ")"
	switch k {
	case KindDate:
		// DATE - DATE is BIGINT; the number parts read DOUBLE.
		return "CASE WHEN " + finite + " THEN CAST(" + x + " - " + f + " AS DOUBLE) END"
	case KindTimestamp:
		return "CASE WHEN " + finite + " THEN epoch(" + x + ") - epoch(" + f + ") END"
	}
	return ""
}

// OffsetAxis returns the SQL expression of the offset axis between the
// quoted columns x and f, where x has type dataType, or "" when the type
// has none.
func OffsetAxis(dataType, x, f string) string { return KindOf(dataType).offsetAxis(x, f) }

// Axis returns the SQL expression of the number axis of the quoted
// column x of type dataType, or "" when the type has none.
func Axis(dataType, x string) string { return KindOf(dataType).axis(x) }

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
