// SPDX-License-Identifier: Apache-2.0

package apitypes

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// TypeRegistry maps (APIVersion, Kind) pairs to Object factories. Callers
// can register custom kinds; DefaultRegistry returns a registry populated
// with all built-in kinds.
type TypeRegistry interface {
	Lookup(tm TypeMeta) (Object, bool)
	Register(tm TypeMeta, factory func() Object)
}

// NewTypeRegistry returns an empty registry.
func NewTypeRegistry() TypeRegistry {
	return &typeRegistry{m: map[TypeMeta]func() Object{}}
}

// DefaultRegistry returns a registry with every built-in kind registered
// under GroupVersionAlpha1 and, for v1-beta types, GroupVersionBeta1.
func DefaultRegistry() TypeRegistry {
	r := NewTypeRegistry()
	alpha := func(k Kind, f func() Object) {
		r.Register(TypeMeta{APIVersion: GroupVersionAlpha1, Kind: k}, f)
	}
	beta := func(k Kind, f func() Object) {
		r.Register(TypeMeta{APIVersion: GroupVersionBeta1, Kind: k}, f)
	}
	alpha(KindSQLAccessPolicy, func() Object { return &SQLAccessPolicy{} })
	alpha(KindRowFilterPolicy, func() Object { return &RowFilterPolicy{} })
	alpha(KindColumnMaskPolicy, func() Object { return &ColumnMaskPolicy{} })
	alpha(KindQueryRejectPolicy, func() Object { return &QueryRejectPolicy{} })
	alpha(KindQueryRewritePolicy, func() Object { return &QueryRewritePolicy{} })
	alpha(KindApprovalPolicy, func() Object { return &ApprovalPolicy{} })
	alpha(KindDataSource, func() Object { return &DataSource{} })
	alpha(KindSubjectBinding, func() Object { return &SubjectBinding{} })
	alpha(KindAuditSink, func() Object { return &AuditSink{} })
	// v1-beta kinds resolved by their own runtime packages.
	beta(KindRelationshipPolicy, func() Object { return &RelationshipPolicy{} })
	beta(KindDataClassification, func() Object { return &DataClassification{} })
	return r
}

type typeRegistry struct {
	m map[TypeMeta]func() Object
}

func (r *typeRegistry) Lookup(tm TypeMeta) (Object, bool) {
	f, ok := r.m[tm]
	if !ok {
		return nil, false
	}
	return f(), true
}

func (r *typeRegistry) Register(tm TypeMeta, factory func() Object) {
	r.m[tm] = factory
}

// ValidationError describes a single decoded-document validation failure.
// When the decoder is driven by internal/config, Line is populated from the
// source file; otherwise it is 0.
type ValidationError struct {
	Kind   Kind
	Name   string
	Field  string
	Reason string
	Line   int // 1-based; 0 if unknown
}

// Error implements error.
func (e *ValidationError) Error() string {
	var b strings.Builder
	if e.Kind != "" {
		b.WriteString(string(e.Kind))
	} else {
		b.WriteString("<unknown kind>")
	}
	if e.Name != "" {
		b.WriteString("/")
		b.WriteString(e.Name)
	}
	if e.Line > 0 {
		fmt.Fprintf(&b, " (line %d)", e.Line)
	}
	if e.Field != "" {
		b.WriteString(": field ")
		b.WriteString(e.Field)
	}
	b.WriteString(": ")
	b.WriteString(e.Reason)
	return b.String()
}

type decodeOptions struct {
	strict   bool
	registry TypeRegistry
}

// DecodeOption configures Decode.
type DecodeOption func(*decodeOptions)

// WithStrictUnknown makes unknown YAML fields a hard error. Default: false.
func WithStrictUnknown(strict bool) DecodeOption {
	return func(o *decodeOptions) { o.strict = strict }
}

// WithRegistry overrides the type registry used during decode.
func WithRegistry(r TypeRegistry) DecodeOption {
	return func(o *decodeOptions) { o.registry = r }
}

// Decode reads a multi-document YAML stream and returns the parsed Objects
// in document order. On parse error for one document, Decode stops and
// returns the partial list together with the error. Callers that want to
// collect all errors should split the stream manually.
func Decode(r io.Reader, opts ...DecodeOption) ([]Object, error) {
	o := &decodeOptions{strict: false, registry: DefaultRegistry()}
	for _, opt := range opts {
		opt(o)
	}

	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("apitypes: read YAML stream: %w", err)
	}

	docs := splitYAMLDocs(buf)
	out := make([]Object, 0, len(docs))

	for i, doc := range docs {
		trimmed := bytes.TrimSpace(doc)
		if len(trimmed) == 0 {
			continue
		}
		obj, err := decodeOne(trimmed, o)
		if err != nil {
			return out, fmt.Errorf("apitypes: document %d: %w", i+1, err)
		}
		if err := validate(obj); err != nil {
			return out, err
		}
		out = append(out, obj)
	}
	return out, nil
}

// decodeOne parses a single YAML document into an Object selected via the
// TypeMeta.
func decodeOne(doc []byte, o *decodeOptions) (Object, error) {
	// First pass: read only the type meta so we can pick a concrete type.
	var tm TypeMeta
	if err := yaml.Unmarshal(doc, &tm); err != nil {
		return nil, fmt.Errorf("read type meta: %w", err)
	}
	if tm.APIVersion == "" || tm.Kind == "" {
		return nil, errors.New("apiVersion and kind are required")
	}
	obj, ok := o.registry.Lookup(tm)
	if !ok {
		return nil, fmt.Errorf("unknown kind %s/%s", tm.APIVersion, tm.Kind)
	}

	// Second pass: decode into the concrete type.
	if o.strict {
		if err := yaml.UnmarshalStrict(doc, obj); err != nil {
			return nil, fmt.Errorf("decode %s: %w", tm.Kind, err)
		}
	} else {
		if err := yaml.Unmarshal(doc, obj); err != nil {
			return nil, fmt.Errorf("decode %s: %w", tm.Kind, err)
		}
	}
	return obj, nil
}

// splitYAMLDocs splits a YAML stream on document markers (`---` on its own
// line). Leading/trailing empty documents are preserved; callers filter
// them out.
func splitYAMLDocs(buf []byte) [][]byte {
	var docs [][]byte
	var cur bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(buf))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if trim == "---" {
			docs = append(docs, append([]byte(nil), cur.Bytes()...))
			cur.Reset()
			continue
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	if cur.Len() > 0 {
		docs = append(docs, append([]byte(nil), cur.Bytes()...))
	}
	return docs
}

// validate runs per-kind structural checks and returns a *ValidationError
// on failure.
func validate(obj Object) error {
	meta := obj.GetObjectMeta()
	tm := obj.GetTypeMeta()
	if meta.Name == "" {
		return &ValidationError{Kind: tm.Kind, Field: "metadata.name", Reason: "required"}
	}
	if !rfc1123Label.MatchString(meta.Name) {
		return &ValidationError{
			Kind: tm.Kind, Name: meta.Name, Field: "metadata.name",
			Reason: "must be an RFC 1123 label (lowercase alphanumeric and dashes, 1-63 chars)",
		}
	}
	if meta.Priority < 0 || meta.Priority > 1000 {
		return &ValidationError{
			Kind: tm.Kind, Name: meta.Name, Field: "metadata.priority",
			Reason: "must be between 0 and 1000",
		}
	}
	// Per-kind structural checks.
	switch x := obj.(type) {
	case *SQLAccessPolicy:
		return validateSQLAccess(x)
	case *RowFilterPolicy:
		return validateRowFilter(x)
	case *ColumnMaskPolicy:
		return validateColumnMask(x)
	case *QueryRejectPolicy:
		return validateQueryReject(x)
	case *QueryRewritePolicy:
		return nil
	case *ApprovalPolicy:
		return validateApproval(x)
	case *RelationshipPolicy:
		return validateRelationship(x)
	case *DataClassification:
		return validateDataClassification(x)
	case *DataSource:
		return validateDataSource(x)
	case *SubjectBinding:
		return validateSubjectBinding(x)
	case *AuditSink:
		return validateAuditSink(x)
	}
	return nil
}

var rfc1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func validateSQLAccess(p *SQLAccessPolicy) error {
	if p.Spec.Effect != EffectAllow && p.Spec.Effect != EffectDeny {
		return &ValidationError{
			Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.effect",
			Reason: `must be "allow" or "deny"`,
		}
	}
	return validateSelector(p.GetKind(), p.Metadata.Name, "spec.match", &p.Spec.Match)
}

func validateRowFilter(p *RowFilterPolicy) error {
	if err := validateSelector(p.GetKind(), p.Metadata.Name, "spec.match", &p.Spec.Match); err != nil {
		return err
	}
	f := p.Spec.Filter
	hasPred := f.Predicate != nil
	hasExpr := f.Expression != ""
	if hasPred == hasExpr {
		return &ValidationError{
			Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.filter",
			Reason: "exactly one of predicate or expression must be set",
		}
	}
	return nil
}

func validateColumnMask(p *ColumnMaskPolicy) error {
	if err := validateSelector(p.GetKind(), p.Metadata.Name, "spec.match", &p.Spec.Match); err != nil {
		return err
	}
	m := p.Spec.Mask
	switch m.Type {
	case MaskCustom:
		if m.Expression == "" {
			return &ValidationError{
				Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.mask.expression",
				Reason: `required when mask.type == "custom"`,
			}
		}
	case MaskPartial:
		if m.Args.ShowFirst <= 0 && m.Args.ShowLast <= 0 {
			return &ValidationError{
				Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.mask.args",
				Reason: "partial mask requires at least one of showFirst or showLast > 0",
			}
		}
	case MaskHash:
		if m.Args.Algorithm == HashHMACSHA256 && m.Args.SaltRef == "" {
			return &ValidationError{
				Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.mask.args.saltRef",
				Reason: `required when algorithm is "hmac_sha256"`,
			}
		}
	case MaskNull, MaskConstant, MaskRegex, MaskTruncate, MaskJitter,
		MaskFPE, MaskFake, MaskExternal:
		// No extra structural check at this layer; per-provider validation
		// lives in pkg/mask and is enforced at apply time.
	default:
		return &ValidationError{
			Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.mask.type",
			Reason: fmt.Sprintf("unknown mask type %q", m.Type),
		}
	}
	return nil
}

// approvalTriggerOps is the set of operators a PredicateTrigger may use
// (plus "" / "*" for any). Kept in lockstep with parser.Comparison ops.
var approvalTriggerOps = map[string]struct{}{
	"": {}, "*": {}, "=": {}, "!=": {}, "<": {}, "<=": {}, ">": {}, ">=": {},
	"like": {}, "ilike": {}, "in": {}, "isnull": {},
}

func validateApproval(p *ApprovalPolicy) error {
	if err := validateSelector(p.GetKind(), p.Metadata.Name, "spec.match", &p.Spec.Match); err != nil {
		return err
	}
	if p.Spec.When != nil {
		for i, tr := range p.Spec.When.Predicates {
			if tr.Column == "" {
				return &ValidationError{
					Kind: p.GetKind(), Name: p.Metadata.Name,
					Field:  fmt.Sprintf("spec.when.predicates[%d].column", i),
					Reason: "column is required",
				}
			}
			if _, ok := approvalTriggerOps[tr.Op]; !ok {
				return &ValidationError{
					Kind: p.GetKind(), Name: p.Metadata.Name,
					Field:  fmt.Sprintf("spec.when.predicates[%d].op", i),
					Reason: fmt.Sprintf("unknown op %q", tr.Op),
				}
			}
		}
	}
	return nil
}

var tagPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)

func validateDataClassification(p *DataClassification) error {
	if len(p.Spec.Rules) == 0 {
		return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.rules", Reason: "at least one rule is required"}
	}
	for i, r := range p.Spec.Rules {
		if len(r.Tags) == 0 {
			return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: fmt.Sprintf("spec.rules[%d].tags", i), Reason: "at least one tag is required"}
		}
		for _, tag := range r.Tags {
			if !tagPattern.MatchString(tag) {
				return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: fmt.Sprintf("spec.rules[%d].tags", i), Reason: fmt.Sprintf("tag %q invalid (use [a-z0-9._-])", tag)}
			}
		}
		if len(r.Resources.Tags) > 0 {
			return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: fmt.Sprintf("spec.rules[%d].resources.tags", i), Reason: "a classification rule may not reference tags (no recursion)"}
		}
		if len(r.Resources.Actions) > 0 {
			return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: fmt.Sprintf("spec.rules[%d].resources.actions", i), Reason: "actions are not allowed in a classification rule"}
		}
	}
	return nil
}

func validateRelationship(p *RelationshipPolicy) error {
	if err := validateSelector(p.GetKind(), p.Metadata.Name, "spec.match", &p.Spec.Match); err != nil {
		return err
	}
	if p.Spec.Backend.Endpoint == "" {
		return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.backend.endpoint", Reason: "required"}
	}
	if p.Spec.Backend.StoreID == "" {
		return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.backend.storeId", Reason: "required"}
	}
	if len(p.Spec.Checks) == 0 {
		return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.checks", Reason: "at least one check is required"}
	}
	for i, c := range p.Spec.Checks {
		if c.ObjectTemplate == "" {
			return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: fmt.Sprintf("spec.checks[%d].objectTemplate", i), Reason: "required"}
		}
		if c.Relation == "" {
			return &ValidationError{Kind: p.GetKind(), Name: p.Metadata.Name, Field: fmt.Sprintf("spec.checks[%d].relation", i), Reason: "required"}
		}
	}
	return nil
}

func validateQueryReject(p *QueryRejectPolicy) error {
	if err := validateSelector(p.GetKind(), p.Metadata.Name, "spec.match", &p.Spec.Match); err != nil {
		return err
	}
	if len(p.Spec.Reject.Rules) == 0 {
		return &ValidationError{
			Kind: p.GetKind(), Name: p.Metadata.Name, Field: "spec.reject.rules",
			Reason: "at least one rule is required",
		}
	}
	return nil
}

func validateDataSource(d *DataSource) error {
	s := d.Spec
	switch s.Type {
	case DSPostgres, DSMySQL:
		if s.Connection == "" {
			return &ValidationError{
				Kind: d.GetKind(), Name: d.Metadata.Name, Field: "spec.connection",
				Reason: "required for postgres/mysql",
			}
		}
	case DSSQLite, DSDuckDBFile:
		if s.Path == "" {
			return &ValidationError{
				Kind: d.GetKind(), Name: d.Metadata.Name, Field: "spec.path",
				Reason: "required for sqlite/duckdb_file",
			}
		}
	case DSS3Parquet:
		if s.Bucket == "" {
			return &ValidationError{
				Kind: d.GetKind(), Name: d.Metadata.Name, Field: "spec.bucket",
				Reason: "required for s3_parquet",
			}
		}
	case DSMotherDuck:
		if s.Database == "" {
			return &ValidationError{
				Kind: d.GetKind(), Name: d.Metadata.Name, Field: "spec.database",
				Reason: "required for motherduck",
			}
		}
	default:
		return &ValidationError{
			Kind: d.GetKind(), Name: d.Metadata.Name, Field: "spec.type",
			Reason: fmt.Sprintf("unknown data source type %q", s.Type),
		}
	}
	return nil
}

func validateSubjectBinding(b *SubjectBinding) error {
	s := b.Spec
	if s.JWKSURL != "" && (s.Issuer == "" || s.Audience == "") {
		return &ValidationError{
			Kind: b.GetKind(), Name: b.Metadata.Name, Field: "spec",
			Reason: "jwksUrl requires both issuer and audience",
		}
	}
	return nil
}

func validateAuditSink(a *AuditSink) error {
	s := a.Spec
	switch s.Type {
	case AuditFile:
		if s.Path == "" {
			return &ValidationError{
				Kind: a.GetKind(), Name: a.Metadata.Name, Field: "spec.path",
				Reason: "required for file sink",
			}
		}
	case AuditS3, AuditPostgres, AuditSyslog, AuditOTLP:
		// Declared by the manifest grammar but no implementation writes
		// records yet. Rejected so an operator never believes durable
		// delivery is configured when it is not; the guard drops per type
		// as each sink lands.
		return &ValidationError{
			Kind: a.GetKind(), Name: a.Metadata.Name, Field: "spec.type",
			Reason: fmt.Sprintf("audit sink type %q parsed but unimplemented — only \"file\" writes records in this build", s.Type),
		}
	default:
		return &ValidationError{
			Kind: a.GetKind(), Name: a.Metadata.Name, Field: "spec.type",
			Reason: fmt.Sprintf("unknown audit sink type %q", s.Type),
		}
	}
	return nil
}

func validateSelector(kind Kind, name, field string, sel *Selector) error {
	if len(sel.Any) == 0 && len(sel.All) == 0 {
		return &ValidationError{
			Kind: kind, Name: name, Field: field,
			Reason: "either any or all must be non-empty",
		}
	}
	return nil
}
