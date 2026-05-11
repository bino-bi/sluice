// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Duration is a time.Duration that marshals/unmarshals as a human-friendly
// string. YAML/JSON accept the same set of representations:
//
//	"30s"   — any time.ParseDuration-compatible string
//	"1h30m"
//	30      — plain integer, interpreted as seconds
//	"0"     — zero duration
//
// Negative values are rejected. The zero value marshals as the empty string.
type Duration time.Duration

// errNegativeDuration is returned when a parsed duration is negative.
var errNegativeDuration = errors.New("apitypes: negative durations are not allowed")

// ParseDuration parses the permitted formats (see Duration docs).
func ParseDuration(s string) (Duration, error) {
	if s == "" {
		return 0, nil
	}
	// Bare integer → seconds.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		if n < 0 {
			return 0, errNegativeDuration
		}
		return Duration(time.Duration(n) * time.Second), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("apitypes: invalid duration %q: %w", s, err)
	}
	if d < 0 {
		return 0, errNegativeDuration
	}
	return Duration(d), nil
}

// Duration returns the value as a standard time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String renders the duration using time.Duration's canonical form.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	if d == 0 {
		return []byte(`""`), nil
	}
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler. Accepts a JSON string or number.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		*d = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		parsed, err := ParseDuration(s)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	}
	// Numeric: seconds.
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	if n < 0 {
		return errNegativeDuration
	}
	*d = Duration(time.Duration(n * float64(time.Second)))
	return nil
}

// MarshalYAML implements yaml.Marshaler (sigs.k8s.io/yaml delegates to JSON,
// so MarshalJSON covers that path; this method is provided for callers that
// use go-yaml directly).
func (d Duration) MarshalYAML() (any, error) {
	if d == 0 {
		return "", nil
	}
	return time.Duration(d).String(), nil
}

// UnmarshalYAML implements yaml.Unmarshaler for go-yaml.
func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case nil:
		*d = 0
		return nil
	case string:
		parsed, err := ParseDuration(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case int:
		if v < 0 {
			return errNegativeDuration
		}
		*d = Duration(time.Duration(v) * time.Second)
		return nil
	case int64:
		if v < 0 {
			return errNegativeDuration
		}
		*d = Duration(time.Duration(v) * time.Second)
		return nil
	case float64:
		if v < 0 {
			return errNegativeDuration
		}
		*d = Duration(time.Duration(v * float64(time.Second)))
		return nil
	default:
		return fmt.Errorf("apitypes: duration must be a string or number, got %T", raw)
	}
}
