// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON implements json.Unmarshaler so MaskArgs captures unknown
// fields into Extras.
//
// sigs.k8s.io/yaml converts YAML → JSON → struct, and encoding/json does
// not honor `yaml:",inline"`. Without this custom unmarshaler, the
// forward-compatibility guarantee (policy files written for future
// sluice versions still parse) would quietly break.
func (m *MaskArgs) UnmarshalJSON(data []byte) error {
	// Decode into a generic map first to separate known from unknown keys.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("apitypes: MaskArgs: %w", err)
	}

	// Decode known fields into a shadow struct (same field layout but
	// without the Extras field, so encoding/json doesn't recurse).
	type shadow struct {
		Value          any      `json:"value,omitempty"`
		ShowFirst      int      `json:"showFirst,omitempty"`
		ShowLast       int      `json:"showLast,omitempty"`
		MaskChar       string   `json:"maskChar,omitempty"`
		Algorithm      HashAlgo `json:"algorithm,omitempty"`
		SaltRef        string   `json:"saltRef,omitempty"`
		Pattern        string   `json:"pattern,omitempty"`
		Replacement    string   `json:"replacement,omitempty"`
		Length         int      `json:"length,omitempty"`
		Suffix         string   `json:"suffix,omitempty"`
		Range          float64  `json:"range,omitempty"`
		Seed           string   `json:"seed,omitempty"`
		KeyRef         string   `json:"keyRef,omitempty"`
		Tweak          string   `json:"tweak,omitempty"`
		Alphabet       string   `json:"alphabet,omitempty"`
		CustomAlphabet string   `json:"customAlphabet,omitempty"`
		FakeType       string   `json:"fakeType,omitempty"`
		Provider       string   `json:"provider,omitempty"`
		KeyPath        string   `json:"keyPath,omitempty"`
	}
	var s shadow
	// Use a stricter decoder so numeric JSON types decode as json.Number
	// and then into int/float64 cleanly.
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&s); err != nil {
		return fmt.Errorf("apitypes: MaskArgs: %w", err)
	}

	*m = MaskArgs{
		Value:          s.Value,
		ShowFirst:      s.ShowFirst,
		ShowLast:       s.ShowLast,
		MaskChar:       s.MaskChar,
		Algorithm:      s.Algorithm,
		SaltRef:        s.SaltRef,
		Pattern:        s.Pattern,
		Replacement:    s.Replacement,
		Length:         s.Length,
		Suffix:         s.Suffix,
		Range:          s.Range,
		Seed:           s.Seed,
		KeyRef:         s.KeyRef,
		Tweak:          s.Tweak,
		Alphabet:       s.Alphabet,
		CustomAlphabet: s.CustomAlphabet,
		FakeType:       s.FakeType,
		Provider:       s.Provider,
		KeyPath:        s.KeyPath,
	}

	// Anything not in the known set spills into Extras.
	for k, v := range raw {
		if _, known := maskArgsKnownKeys[k]; known {
			continue
		}
		if m.Extras == nil {
			m.Extras = map[string]any{}
		}
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return fmt.Errorf("apitypes: MaskArgs extras[%q]: %w", k, err)
		}
		m.Extras[k] = decoded
	}
	return nil
}
