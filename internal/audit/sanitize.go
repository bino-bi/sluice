// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"strings"
	"unicode/utf8"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// sanitizeRecord replaces invalid UTF-8 in every string of r with U+FFFD.
// encoding/json writes an invalid byte as the escape �, but a decoded
// record marshals the rune back as raw bytes. So the hash of a record with
// invalid UTF-8 would not verify.
func sanitizeRecord(r *Record) {
	r.EventType = EventType(validUTF8(string(r.EventType)))
	r.QueryID = validUTF8(r.QueryID)
	r.Subject.ID = validUTF8(r.Subject.ID)
	r.Subject.Method = validUTF8(r.Subject.Method)
	r.Subject.Issuer = validUTF8(r.Subject.Issuer)
	r.Subject.Email = validUTF8(r.Subject.Email)
	r.Subject.Groups = validStrings(r.Subject.Groups)
	r.Subject.RemoteIP = validUTF8(r.Subject.RemoteIP)
	r.Origin = validUTF8(r.Origin)
	r.SQLFingerprint = validUTF8(r.SQLFingerprint)
	r.RewrittenFingerprint = validUTF8(r.RewrittenFingerprint)
	r.SQLSample = validUTF8(r.SQLSample)
	r.Tables = validStrings(r.Tables)
	r.Catalogs = validStrings(r.Catalogs)
	r.PoliciesApplied = validPolicies(r.PoliciesApplied)
	r.Decision = validUTF8(r.Decision)
	r.ErrorCode = validUTF8(r.ErrorCode)
	r.SluiceVersion = validUTF8(r.SluiceVersion)
	r.DuckDBVersion = validUTF8(r.DuckDBVersion)
	r.ParserVersion = validUTF8(r.ParserVersion)
	r.RemoteIP = validUTF8(r.RemoteIP)
	r.BindingName = validUTF8(r.BindingName)
	r.Message = validUTF8(r.Message)
	if r.Extras != nil {
		r.Extras = validValue(r.Extras).(map[string]any)
	}
	if r.ClientMeta != nil {
		meta := make(map[string]string, len(r.ClientMeta))
		for k, v := range r.ClientMeta {
			meta[validUTF8(k)] = validUTF8(v)
		}
		r.ClientMeta = meta
	}
	r.PoliciesShadow = validPolicies(r.PoliciesShadow)
	r.HashAlg = validUTF8(r.HashAlg)
	r.PriorHash = validUTF8(r.PriorHash)
}

func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// validStrings copies ss only when a string changes: the caller may share
// the slice.
func validStrings(ss []string) []string {
	var out []string
	for i, s := range ss {
		v := validUTF8(s)
		if v == s {
			continue
		}
		if out == nil {
			out = append([]string(nil), ss...)
		}
		out[i] = v
	}
	if out == nil {
		return ss
	}
	return out
}

func validPolicies(ps []apitypes.AppliedPolicy) []apitypes.AppliedPolicy {
	if len(ps) == 0 {
		return ps
	}
	out := make([]apitypes.AppliedPolicy, len(ps))
	for i, p := range ps {
		p.Kind = apitypes.Kind(validUTF8(string(p.Kind)))
		p.Name = validUTF8(p.Name)
		p.Would = validUTF8(p.Would)
		out[i] = p
	}
	return out
}

// validValue returns v with valid UTF-8 in the strings and keys of the
// JSON shapes extras use. Maps and slices are copied.
func validValue(v any) any {
	switch x := v.(type) {
	case string:
		return validUTF8(x)
	case []string:
		return validStrings(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = validValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[validUTF8(k)] = validValue(e)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, e := range x {
			out[validUTF8(k)] = validUTF8(e)
		}
		return out
	default:
		return v
	}
}
