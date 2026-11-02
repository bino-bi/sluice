// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/bino-bi/sluice/internal/synth/profile"
	"github.com/bino-bi/sluice/pkg/apitypes"
)

// Stage is the simple type a column is staged in before the builder
// casts it to the snapshot type. Each stage has one Go type.
type Stage int

// Stages and their Go types.
const (
	StageVarchar   Stage = iota // string
	StageBigint                 // int64
	StageDouble                 // float64
	StageBoolean                // bool
	StageDate                   // time.Time, midnight UTC
	StageTimestamp              // time.Time, UTC
)

// SQL returns the DuckDB type of the stage.
func (s Stage) SQL() string {
	return [...]string{"VARCHAR", "BIGINT", "DOUBLE", "BOOLEAN", "DATE", "TIMESTAMP"}[s]
}

// StageOf returns the stage of a column with generator g. bucket writes
// text. Integers above 2^63 stage as DOUBLE, TIME as DOUBLE seconds of
// the day, UUID and ENUM as VARCHAR.
func StageOf(col Column, g apitypes.SyntheticGenerator) Stage {
	if g == apitypes.GenBucket {
		return StageVarchar
	}
	switch col.Kind() {
	case profile.KindInteger:
		switch strings.ToUpper(strings.TrimSpace(col.Type)) {
		case "HUGEINT", "UHUGEINT", "UBIGINT":
			return StageDouble
		}
		return StageBigint
	case profile.KindFloat, profile.KindTime:
		return StageDouble
	case profile.KindBool:
		return StageBoolean
	case profile.KindDate:
		return StageDate
	case profile.KindTimestamp:
		return StageTimestamp
	}
	return StageVarchar
}

const secondsPerDay = 86400

// fromAxis converts a value on the number axis of the column kind
// (profile.Kind.Numeric) to the stage type.
func fromAxis(st Stage, v float64) any {
	switch st {
	case StageBigint:
		return int64(math.Round(v))
	case StageDate:
		return time.Unix(int64(math.Floor(v))*secondsPerDay, 0).UTC()
	case StageTimestamp:
		return time.UnixMicro(int64(math.Round(float64(v * 1e6)))).UTC()
	case StageVarchar:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return v
}

// toAxis converts a staged date or timestamp to its number axis.
func toAxis(st Stage, t time.Time) float64 {
	if st == StageDate {
		return float64(t.Unix() / secondsPerDay)
	}
	return float64(t.UnixMicro()) / 1e6
}

var errStage = errors.New("value does not fit the column type")

// stageValue converts a value as duckdb-go scans it to the stage type.
// The error never holds the value.
func stageValue(st Stage, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if t, ok := v.(time.Time); ok {
		switch st {
		case StageDate:
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
		case StageTimestamp:
			return t.UTC(), nil
		case StageDouble:
			// TIME scans as a time on 0001-01-01.
			h, m, s := t.Clock()
			return float64(h*3600+m*60+s) + float64(t.Nanosecond()/1000)/1e6, nil
		case StageVarchar:
			return t.UTC().Format(time.RFC3339Nano), nil
		}
		return nil, errStage
	}
	switch st {
	case StageVarchar:
		switch x := v.(type) {
		case string:
			return x, nil
		case []byte:
			if len(x) == 16 {
				return formatUUID(x), nil
			}
			return string(x), nil
		case fmt.Stringer:
			return x.String(), nil
		}
		return fmt.Sprint(v), nil
	case StageBoolean:
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, errStage
	case StageBigint, StageDouble:
		f, isInt, i, ok := number(v)
		if !ok {
			return nil, errStage
		}
		if st == StageDouble {
			return f, nil
		}
		if !isInt {
			return int64(math.Round(f)), nil
		}
		return i, nil
	}
	return nil, errStage
}

// number reads any number type duckdb-go scans: Go ints and floats,
// *big.Int (HUGEINT) and Decimal (via Float64).
func number(v any) (f float64, isInt bool, i int64, ok bool) {
	switch x := v.(type) {
	case int8:
		return float64(x), true, int64(x), true
	case int16:
		return float64(x), true, int64(x), true
	case int32:
		return float64(x), true, int64(x), true
	case int64:
		return float64(x), true, x, true
	case int:
		return float64(x), true, int64(x), true
	case uint8:
		return float64(x), true, int64(x), true
	case uint16:
		return float64(x), true, int64(x), true
	case uint32:
		return float64(x), true, int64(x), true
	case uint64:
		return float64(x), x <= math.MaxInt64, int64(x), true
	case float32:
		return float64(x), false, 0, true
	case float64:
		return x, false, 0, true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, x.IsInt64(), x.Int64(), true
	case interface{ Float64() float64 }:
		return x.Float64(), false, 0, true
	}
	return 0, false, 0, false
}

// compareStaged orders two values of one stage. Mixed types (which a
// stage never has) compare by their text.
func compareStaged(a, b any) int {
	switch x := a.(type) {
	case int64:
		if y, ok := b.(int64); ok {
			return cmp3(x < y, x > y)
		}
	case float64:
		if y, ok := b.(float64); ok {
			return cmp3(x < y, x > y)
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	case bool:
		if y, ok := b.(bool); ok {
			return cmp3(!x && y, x && !y)
		}
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func cmp3(less, more bool) int {
	switch {
	case less:
		return -1
	case more:
		return 1
	}
	return 0
}

// specString renders a scalar from the spec for Column.Cast.
func specString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}

// specValue casts a spec scalar to the column type and then to the
// stage type.
func specValue(col Column, st Stage, v any) (any, error) {
	if st == StageVarchar {
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	if col.Cast == nil {
		return nil, errors.New("no cast for spec values")
	}
	c, err := col.Cast(specString(v))
	if err != nil {
		return nil, errStage
	}
	return stageValue(st, c)
}
