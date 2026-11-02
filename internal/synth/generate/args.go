// SPDX-License-Identifier: AGPL-3.0-or-later

package generate

import (
	"math"

	"github.com/bino-bi/sluice/internal/synth/profile"
)

func (a Args) num(k string) (float64, bool) {
	v, ok := a[k].(float64)
	return v, ok
}

func (a Args) intOr(k string, def int) int {
	if v, ok := a[k].(float64); ok {
		return int(v)
	}
	return def
}

func (a Args) boolOr(k string, def bool) bool {
	if v, ok := a[k].(bool); ok {
		return v
	}
	return def
}

func (a Args) strOr(k, def string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return def
}

// isSource reports whether arg k is "source" or unset with default
// "source".
func (a Args) isSource(k string, def bool) bool {
	v, set := a[k]
	if !set {
		return def
	}
	s, ok := v.(string)
	return ok && s == "source"
}

// span is a length arg: "source", a fixed number, or {min, max}.
type span struct {
	source   bool
	min, max int
}

func (a Args) span(k string) span {
	switch v := a[k].(type) {
	case float64:
		return span{min: int(v), max: int(v)}
	case map[string]any:
		lo, _ := v["min"].(float64)
		hi, _ := v["max"].(float64)
		return span{min: int(lo), max: int(hi)}
	}
	return span{source: true}
}

// rounding is the decimals and integer args of a number generator.
type rounding struct {
	// decimals < 0 means no rounding.
	decimals int
}

func roundingOf(col Column, a Args) rounding {
	if a.boolOr("integer", col.Kind() == profile.KindInteger) {
		return rounding{decimals: 0}
	}
	return rounding{decimals: a.intOr("decimals", -1)}
}

// unit is the smallest step of the rounded values, for profile.Needs.
func (r rounding) unit() float64 {
	if r.decimals < 0 {
		return 0
	}
	return math.Pow10(-r.decimals)
}

// fit clamps v to [lo, hi] and rounds it. A rounded value outside the
// range is rounded toward the inside instead.
func (r rounding) fit(v, lo, hi float64) float64 {
	v = min(max(v, lo), hi)
	if r.decimals < 0 {
		return v
	}
	m := math.Pow10(r.decimals)
	out := math.Round(float64(v*m)) / m
	if out < lo {
		out = math.Ceil(float64(lo*m)) / m
	}
	if out > hi {
		out = math.Floor(float64(hi*m)) / m
	}
	return out
}

// pool is the number of rounded values in [lo, hi], or -1.
func (r rounding) pool(lo, hi float64) int64 {
	if r.decimals < 0 || hi < lo {
		return -1
	}
	m := math.Pow10(r.decimals)
	n := math.Floor(float64(hi*m)) - math.Ceil(float64(lo*m)) + 1
	if n > math.MaxInt32 {
		return -1
	}
	return int64(max(n, 1))
}
