// SPDX-License-Identifier: AGPL-3.0-or-later

package opaengine

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/bino-bi/sluice/internal/parser"
	"github.com/bino-bi/sluice/internal/policy"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// buildInput assembles the rego input document from the request. It is
// never logged — claims can be sensitive.
func buildInput(in policy.Input) map[string]any {
	subject := map[string]any{"id": "", "issuer": "", "email": "", "groups": []string{}, "auth_method": "", "claims": map[string]any{}}
	if in.User != nil {
		subject["id"] = in.User.Subject
		subject["issuer"] = in.User.Issuer
		subject["email"] = in.User.Email
		subject["groups"] = in.User.Groups
		subject["auth_method"] = string(in.User.AuthMethod)
		if in.User.Claims != nil {
			subject["claims"] = in.User.Claims
		}
	}
	tables := make([]map[string]any, 0, len(in.Tables))
	for _, t := range in.Tables {
		tables = append(tables, map[string]any{
			"catalog": t.Catalog, "schema": t.Schema, "table": t.Table,
			"key": tableKey(t),
		})
	}
	action := ""
	if in.AST != nil {
		action = string(in.AST.Statement())
	}
	request := map[string]any{"remote_ip": "", "user_agent": "", "headers": map[string]string{}}
	if in.Request != nil {
		if in.Request.RemoteIP != nil {
			request["remote_ip"] = in.Request.RemoteIP.String()
		}
		request["user_agent"] = in.Request.UserAgent
		if in.Request.Headers != nil {
			request["headers"] = in.Request.Headers
		}
	}
	return map[string]any{
		"subject": subject,
		"action":  action,
		"tables":  tables,
		"shape":   shapeDoc(in.Shape),
		"request": request,
	}
}

func shapeDoc(s parser.QueryShape) map[string]any {
	return map[string]any{
		"has_select_star": s.HasSelectStar,
		"is_aggregate":    s.IsAggregate,
		"has_cte":         s.HasCTE,
		"has_union":       s.HasUnion,
		"has_limit":       s.HasLimit,
		"limit":           s.LimitValue,
		"has_where":       s.HasWhere,
		"joins":           s.Joins,
	}
}

func tableKey(t parser.TableRef) string {
	return t.Catalog + "." + t.Schema + "." + t.Table
}

// outputDoc is the rego decision contract.
type outputDoc struct {
	Allow       bool            `json:"allow"`
	Abstain     bool            `json:"abstain"`
	DenyReason  *outDenyReason  `json:"deny_reason"`
	RowFilters  []outRowFilter  `json:"row_filters"`
	ColumnMasks []outColumnMask `json:"column_masks"`
	Rejections  []outRejection  `json:"rejections"`
}

type outDenyReason struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Policy  string `json:"policy"`
}

type outRowFilter struct {
	Table     string              `json:"table"`
	Combine   string              `json:"combine"`
	Predicate *apitypes.Predicate `json:"predicate"`
}

type outColumnMask struct {
	Table  string            `json:"table"`
	Column string            `json:"column"`
	Mask   apitypes.MaskSpec `json:"mask"`
}

type outRejection struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// decodeDecision strictly decodes the rego output into a policy.Decision.
// Any violation of the contract (undefined allow, unknown table, invalid
// predicate/mask) is an error — never a silent allow.
func decodeDecision(raw []byte, tables []parser.TableRef) (*policy.Decision, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var out outputDoc
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("opaengine: output does not match contract: %w", err)
	}

	if out.Abstain {
		d := abstainDeny("opa: abstained")
		d.Abstained = true
		return d, nil
	}
	if !out.Allow {
		reason := &policy.DenyReason{Code: "ACL_DENIED", Message: "denied by OPA policy"}
		if out.DenyReason != nil {
			if out.DenyReason.Message != "" {
				reason.Message = out.DenyReason.Message
			}
			reason.Code = normalizeCode(out.DenyReason.Code, "ACL_DENIED")
			reason.PolicyName = out.DenyReason.Policy
		}
		return &policy.Decision{Outcome: policy.OutcomeDeny, DenyReason: reason}, nil
	}

	valid := tableKeySet(tables)
	d := &policy.Decision{
		Outcome:     policy.OutcomeAllow,
		RowFilters:  map[string]*policy.CompiledFilter{},
		ColumnMasks: map[string]*policy.CompiledMask{},
		Applied:     []apitypes.AppliedPolicy{{Kind: "OpaModule", Name: "opa"}},
	}

	for _, rf := range out.RowFilters {
		if _, ok := valid[rf.Table]; !ok {
			return nil, fmt.Errorf("opaengine: row_filter references table %q not in the input set", rf.Table)
		}
		pred, err := policy.CompilePredicateSpec(rf.Predicate)
		if err != nil {
			return nil, fmt.Errorf("opaengine: row_filter predicate: %w", err)
		}
		combine := apitypes.Combine(rf.Combine)
		if combine == "" {
			combine = apitypes.CombineRestrictive
		}
		d.RowFilters[rf.Table] = policy.NewFilter(rf.Table, pred, combine, "opa")
	}

	for _, cm := range out.ColumnMasks {
		if _, ok := valid[cm.Table]; !ok {
			return nil, fmt.Errorf("opaengine: column_mask references table %q not in the input set", cm.Table)
		}
		args, err := policy.CompileMaskSpec(cm.Mask)
		if err != nil {
			return nil, fmt.Errorf("opaengine: column_mask: %w", err)
		}
		key := cm.Table + "." + cm.Column
		d.ColumnMasks[key] = policy.NewMask(cm.Table, cm.Column, cm.Mask.Type, args, "opa")
	}

	for _, rj := range out.Rejections {
		d.Rejections = append(d.Rejections, policy.Rejection{
			PolicyName: "opa", RuleName: rj.Rule, Message: rj.Message,
			Code: normalizeCode(rj.Code, "ACL_REJECTED"),
		})
	}
	if len(d.Rejections) > 0 {
		d.Outcome = policy.OutcomeReject
	}
	return d, nil
}

func tableKeySet(tables []parser.TableRef) map[string]struct{} {
	out := make(map[string]struct{}, len(tables))
	for _, t := range tables {
		out[tableKey(t)] = struct{}{}
	}
	return out
}

// normalizeCode forces an unknown code back to a known default so a rego
// author cannot mint an arbitrary client-facing error code.
func normalizeCode(code, fallback string) string {
	if code == "" {
		return fallback
	}
	for _, c := range pkgerr.AllCodes() {
		if string(c) == code {
			return code
		}
	}
	if code == "ACL_DENIED" || code == "ACL_REJECTED" {
		return code
	}
	return fallback
}
