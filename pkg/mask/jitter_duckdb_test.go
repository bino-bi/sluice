// SPDX-License-Identifier: Apache-2.0

package mask_test

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/duckdb/duckdb-go/v2"

	"github.com/bino-bi/sluice/pkg/mask"
)

// TestJitter_DuckDBColumnTypes scans real DuckDB numeric columns into
// *any, the way the executor does, and jitters every value.
func TestJitter_DuckDBColumnTypes(t *testing.T) {
	db := openDuckDB(t)
	p, _ := mask.Default().Lookup("jitter")
	rm, err := p.(mask.RowMasker).NewRowMask(mask.RowMaskContext{
		Ctx: context.Background(), Args: mask.Args{Range: 0.1, Seed: "s"},
	})
	if err != nil {
		t.Fatalf("NewRowMask: %v", err)
	}
	for _, typ := range []string{
		"TINYINT", "SMALLINT", "INTEGER", "BIGINT", "HUGEINT",
		"UTINYINT", "USMALLINT", "UINTEGER", "UBIGINT", "UHUGEINT",
		"FLOAT", "DOUBLE", "DECIMAL(10,2)", "DECIMAL(4,2)", "DECIMAL(38,3)",
	} {
		t.Run(typ, func(t *testing.T) {
			var v any
			if err := db.QueryRow("SELECT 99::" + typ).Scan(&v); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out, err := rm.Mask(v)
			if err != nil {
				t.Fatalf("Mask(%T): %v", v, err)
			}
			if reflect.TypeOf(out) != reflect.TypeOf(v) {
				t.Fatalf("type changed: %T -> %T", v, out)
			}
			got := toFloat(t, out)
			if got < 89 || got > 109 {
				t.Errorf("jitter(99) = %v, outside 10 percent", got)
			}
			if d, ok := out.(duckdb.Decimal); ok {
				in := v.(duckdb.Decimal)
				if d.Scale != in.Scale || d.Width != in.Width {
					t.Errorf("decimal width/scale changed: %d,%d -> %d,%d", in.Width, in.Scale, d.Width, d.Scale)
				}
			}
		})
	}
}

func TestJitter_ClampsToTypeRange(t *testing.T) {
	db := openDuckDB(t)
	p, _ := mask.Default().Lookup("jitter")
	rm, _ := p.(mask.RowMasker).NewRowMask(mask.RowMaskContext{
		Ctx: context.Background(), Args: mask.Args{Range: 0.9, Seed: "s"},
	})
	// Different values give different factors, so some go above the limit.
	for i := 0; i < 20; i++ {
		for _, q := range []string{
			fmt.Sprintf("SELECT %d::TINYINT", 108+i),
			fmt.Sprintf("SELECT %d::UTINYINT", 236+i),
			fmt.Sprintf("SELECT %d.%02d::DECIMAL(4,2)", 99, 80+i),
		} {
			var v any
			if err := db.QueryRow(q).Scan(&v); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			out, err := rm.Mask(v)
			if err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			switch o := out.(type) {
			case int8, uint8:
				// A wrap gives a small or negative number; 0.1 * 108 is above 10.
				if toFloat(t, o) < 10 {
					t.Errorf("%s: %v wrapped", q, o)
				}
			case duckdb.Decimal:
				if o.Value.CmpAbs(big.NewInt(9999)) > 0 {
					t.Errorf("%s: %v exceeds DECIMAL(4,2)", q, o.Value)
				}
			}
		}
	}
}

func toFloat(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	case float32:
		return float64(x)
	case float64:
		return x
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f
	case duckdb.Decimal:
		return x.Float64()
	}
	t.Fatalf("unexpected type %T", v)
	return 0
}

func TestJitter_ClampsHugeAndFloat32(t *testing.T) {
	p, _ := mask.Default().Lookup("jitter")
	rm, _ := p.(mask.RowMasker).NewRowMask(mask.RowMaskContext{
		Ctx: context.Background(), Args: mask.Args{Range: 0.9, Seed: "s"},
	})
	maxHuge := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	for i := int64(0); i < 20; i++ {
		v := new(big.Int).Sub(maxHuge, big.NewInt(i))
		out, err := rm.Mask(v)
		if err != nil {
			t.Fatal(err)
		}
		if out.(*big.Int).Cmp(maxHuge) > 0 {
			t.Errorf("%v: %v exceeds HUGEINT", v, out)
		}
		f, err := rm.Mask(float32(math.MaxFloat32) - float32(i))
		if err != nil {
			t.Fatal(err)
		}
		if math.IsInf(float64(f.(float32)), 0) {
			t.Errorf("float32 near max became %v", f)
		}
	}
}
