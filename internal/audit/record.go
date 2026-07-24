// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// EventType names the lifecycle event carried by a Record.
type EventType string

// EventType constants.
const (
	EventQuery          EventType = "query"
	EventQueryResult    EventType = "query-result"
	EventPolicyDecision EventType = "policy-decision"
	EventConfigReload   EventType = "config-reload"
	EventAuthEvent      EventType = "auth-event"
	EventAdminAction    EventType = "admin-action"
	EventDatasourceConn EventType = "datasource-connect"
	EventGenesis        EventType = "genesis"
	// Approval-workflow lifecycle events (additive; best-effort emits —
	// the data-serving record is still the fail-closed access record).
	EventApprovalRequested EventType = "approval-requested"
	EventApprovalApproved  EventType = "approval-approved"
	EventApprovalRejected  EventType = "approval-rejected"
	EventApprovalExpired   EventType = "approval-expired"
)

// Decision labels used in Record.Decision. They mirror policy.Outcome plus
// an "error" label for audit records that finalise a failed execution.
const (
	DecisionAllow  = "allow"
	DecisionDeny   = "deny"
	DecisionReject = "reject"
	DecisionError  = "error"
)

// Subject is the snapshot of the authenticated principal at the moment the
// record was produced. It duplicates the identity.UserCtx fields the audit
// consumer needs, so records remain standalone documents.
type Subject struct {
	ID       string   `json:"id,omitempty"`
	Method   string   `json:"method,omitempty"`
	Issuer   string   `json:"issuer,omitempty"`
	Email    string   `json:"email,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	RemoteIP string   `json:"remote_ip,omitempty"`
}

// Record is one line in the audit chain. Fields are JSON-tagged so the
// on-disk representation is stable across versions. New optional fields
// must be added at the end with `omitempty` so existing chains keep
// verifying.
type Record struct {
	Timestamp            time.Time                `json:"timestamp"`
	EventType            EventType                `json:"event_type"`
	QueryID              string                   `json:"query_id,omitempty"`
	Subject              Subject                  `json:"subject"`
	Origin               string                   `json:"origin,omitempty"`
	SQLFingerprint       string                   `json:"sql_fingerprint,omitempty"`
	RewrittenFingerprint string                   `json:"rewritten_fingerprint,omitempty"`
	SQLSample            string                   `json:"sql_sample,omitempty"`
	Tables               []string                 `json:"tables,omitempty"`
	Catalogs             []string                 `json:"catalogs,omitempty"`
	PoliciesApplied      []apitypes.AppliedPolicy `json:"policies_applied,omitempty"`
	Decision             string                   `json:"decision,omitempty"`
	ErrorCode            string                   `json:"error_code,omitempty"`
	RowCount             int64                    `json:"row_count,omitempty"`
	Truncated            bool                     `json:"truncated,omitempty"`
	DurationMs           int64                    `json:"duration_ms,omitempty"`
	SluiceVersion        string                   `json:"sluice_version,omitempty"`
	DuckDBVersion        string                   `json:"duckdb_version,omitempty"`
	ParserVersion        string                   `json:"parser_version,omitempty"`
	RemoteIP             string                   `json:"remote_ip,omitempty"`
	BindingName          string                   `json:"binding_name,omitempty"`
	Message              string                   `json:"message,omitempty"`
	Extras               map[string]any           `json:"extras,omitempty"`
	// ClientMeta echoes the caller-supplied QueryRequest.Meta pairs,
	// capped by the queryservice. Recorded verbatim: treat as
	// operator-trust-domain data, like sql_sample.
	ClientMeta map[string]string `json:"client_meta,omitempty"`

	PriorHash string `json:"prior_hash"`
	Hash      string `json:"hash,omitempty"`
}

// canonicalView is the ordered key/value slice used by CanonicalJSON. Using
// a slice rather than encoding/json + json.RawMessage gives us full control
// over ordering and omission, which is required for hash stability.
//
// The hash field is intentionally excluded because ComputeHash is defined
// as sha256(prior || "\n" || canonicalJSON(record - hash)).
type canonicalField struct {
	key string
	raw []byte
}

// CanonicalJSON serialises r into w with sorted keys, compact encoding,
// and the `hash` field omitted. Time values render as RFC 3339 with
// nanosecond precision; strings, numbers, booleans use standard JSON.
//
// CanonicalJSON is stable: shuffling map insertion order, re-ordering
// struct fields, or re-encoding an existing record yields identical bytes.
// The package tests enforce this.
func CanonicalJSON(w io.Writer, r *Record) error {
	fields, err := canonicalFields(r)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte{'{'}); err != nil {
		return err
	}
	for i, f := range fields {
		if i > 0 {
			if _, err := w.Write([]byte{','}); err != nil {
				return err
			}
		}
		kjson, kerr := json.Marshal(f.key)
		if kerr != nil {
			return kerr
		}
		if _, err := w.Write(kjson); err != nil {
			return err
		}
		if _, err := w.Write([]byte{':'}); err != nil {
			return err
		}
		if _, err := w.Write(f.raw); err != nil {
			return err
		}
	}
	_, err = w.Write([]byte{'}'})
	return err
}

// canonicalFields walks the Record, skips zero-valued optional fields, and
// returns the resulting (key, encoded value) pairs sorted by key.
func canonicalFields(r *Record) ([]canonicalField, error) {
	out := make([]canonicalField, 0, 24)

	add := func(key string, raw []byte) {
		out = append(out, canonicalField{key: key, raw: raw})
	}
	addJSON := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("audit: marshal %s: %w", key, err)
		}
		add(key, b)
		return nil
	}

	// Required fields (always present).
	if err := addJSON("timestamp", r.Timestamp.UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	if err := addJSON("event_type", string(r.EventType)); err != nil {
		return nil, err
	}
	if err := addJSON("prior_hash", r.PriorHash); err != nil {
		return nil, err
	}
	if err := addSubject(r.Subject, add); err != nil {
		return nil, err
	}

	if r.QueryID != "" {
		if err := addJSON("query_id", r.QueryID); err != nil {
			return nil, err
		}
	}
	if r.Origin != "" {
		if err := addJSON("origin", r.Origin); err != nil {
			return nil, err
		}
	}
	if r.SQLFingerprint != "" {
		if err := addJSON("sql_fingerprint", r.SQLFingerprint); err != nil {
			return nil, err
		}
	}
	if r.RewrittenFingerprint != "" {
		if err := addJSON("rewritten_fingerprint", r.RewrittenFingerprint); err != nil {
			return nil, err
		}
	}
	if r.SQLSample != "" {
		if err := addJSON("sql_sample", r.SQLSample); err != nil {
			return nil, err
		}
	}
	if len(r.Tables) > 0 {
		if err := addJSON("tables", r.Tables); err != nil {
			return nil, err
		}
	}
	if len(r.Catalogs) > 0 {
		if err := addJSON("catalogs", r.Catalogs); err != nil {
			return nil, err
		}
	}
	if len(r.PoliciesApplied) > 0 {
		if err := addJSON("policies_applied", r.PoliciesApplied); err != nil {
			return nil, err
		}
	}
	if r.Decision != "" {
		if err := addJSON("decision", r.Decision); err != nil {
			return nil, err
		}
	}
	if r.ErrorCode != "" {
		if err := addJSON("error_code", r.ErrorCode); err != nil {
			return nil, err
		}
	}
	if r.RowCount != 0 {
		if err := addJSON("row_count", r.RowCount); err != nil {
			return nil, err
		}
	}
	if r.Truncated {
		if err := addJSON("truncated", r.Truncated); err != nil {
			return nil, err
		}
	}
	if r.DurationMs != 0 {
		if err := addJSON("duration_ms", r.DurationMs); err != nil {
			return nil, err
		}
	}
	if r.SluiceVersion != "" {
		if err := addJSON("sluice_version", r.SluiceVersion); err != nil {
			return nil, err
		}
	}
	if r.DuckDBVersion != "" {
		if err := addJSON("duckdb_version", r.DuckDBVersion); err != nil {
			return nil, err
		}
	}
	if r.ParserVersion != "" {
		if err := addJSON("parser_version", r.ParserVersion); err != nil {
			return nil, err
		}
	}
	if r.RemoteIP != "" {
		if err := addJSON("remote_ip", r.RemoteIP); err != nil {
			return nil, err
		}
	}
	if r.BindingName != "" {
		if err := addJSON("binding_name", r.BindingName); err != nil {
			return nil, err
		}
	}
	if r.Message != "" {
		if err := addJSON("message", r.Message); err != nil {
			return nil, err
		}
	}
	if len(r.Extras) > 0 {
		if err := addExtras(r.Extras, add); err != nil {
			return nil, err
		}
	}
	if len(r.ClientMeta) > 0 {
		// json.Marshal sorts string-keyed maps, so this is canonical.
		if err := addJSON("client_meta", r.ClientMeta); err != nil {
			return nil, err
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out, nil
}

// addSubject emits `subject` with its own keys sorted. Subject is a single
// JSON object rather than a top-level flattening so the record shape stays
// recognisable to external consumers.
func addSubject(s Subject, add func(string, []byte)) error {
	pairs := make([]canonicalField, 0, 6)
	push := func(key, value string) error {
		if value == "" {
			return nil
		}
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		pairs = append(pairs, canonicalField{key: key, raw: b})
		return nil
	}
	if err := push("id", s.ID); err != nil {
		return err
	}
	if err := push("method", s.Method); err != nil {
		return err
	}
	if err := push("issuer", s.Issuer); err != nil {
		return err
	}
	if err := push("email", s.Email); err != nil {
		return err
	}
	if len(s.Groups) > 0 {
		b, err := json.Marshal(s.Groups)
		if err != nil {
			return err
		}
		pairs = append(pairs, canonicalField{key: "groups", raw: b})
	}
	if err := push("remote_ip", s.RemoteIP); err != nil {
		return err
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, p := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(p.key)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(p.raw)
	}
	buf.WriteByte('}')
	add("subject", buf.Bytes())
	return nil
}

// addExtras emits `extras` as a sorted-key object. The values are marshalled
// with json.Marshal so callers should stick to JSON-compatible types.
func addExtras(ex map[string]any, add func(string, []byte)) error {
	keys := make([]string, 0, len(ex))
	for k := range ex {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(ex[k])
		if err != nil {
			return fmt.Errorf("audit: marshal extras[%q]: %w", k, err)
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	add("extras", buf.Bytes())
	return nil
}

// MarshalLine returns a single audit line suitable for appending to a file
// sink: canonical JSON with the hash field appended, followed by a newline.
// The resulting bytes form the on-disk record.
func MarshalLine(r *Record) ([]byte, error) {
	var buf bytes.Buffer
	// Canonical prefix ends with `}` — strip it, append `,"hash":"…"}` + \n.
	if err := CanonicalJSON(&buf, r); err != nil {
		return nil, err
	}
	body := buf.Bytes()
	if n := len(body); n == 0 || body[n-1] != '}' {
		return nil, fmt.Errorf("audit: canonical body malformed")
	}
	out := make([]byte, 0, len(body)+32)
	out = append(out, body[:len(body)-1]...)
	if len(body) > 2 { // not an empty object
		out = append(out, ',')
	}
	hashBytes, err := json.Marshal(r.Hash)
	if err != nil {
		return nil, err
	}
	out = append(out, []byte(`"hash":`)...)
	out = append(out, hashBytes...)
	out = append(out, '}', '\n')
	return out, nil
}
