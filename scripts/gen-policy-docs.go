// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build ignore

// gen-policy-docs renders docs/reference/policy-schema.md from
// pkg/apitypes. Read from code: the kinds and the enum values (const
// blocks, via go/ast), the apiVersion of each kind (DefaultRegistry),
// fields and types (reflection on the Spec structs). Written by hand
// below: the "Required" and "Meaning" cells. A field without an entry
// fails the run. Run via `make docs-generate`; `make docs-check` fails
// when the page differs.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// skip lists what the code declares but does not act on. A key is a
// kind, a field as in docs, or Type=value for an enum value.
var skip = map[string]bool{
	"AuditSink":                           true, // decoded, never read: sinks come from the server config
	"object.metadata.namespace":           true, // no reader
	"MaskType=custom":                     true, // compile refuses it
	"MaskType=external":                   true, // compile refuses it
	"ColumnMaskPolicy.mask.expression":    true, // only read by mask type custom
	"ColumnMaskPolicy.mask.args.provider": true, // only read by mask type external
	"ColumnMaskPolicy.mask.args.keyPath":  true, // only read by mask type external
	"QueryRewritePolicy.rewrite.hint":     true, // compile refuses it
	"DataSource.prefix":                   true, // the s3 driver stores it and never uses it
	"DataSource.schemas":                  true, // no driver reads it
	"DataSource.tables":                   true, // no driver reads it
	"DataSource.attachMode":               true, // no driver reads it
	"DataSource.healthCheck":              true, // no reader
	"DataSource.readonly":                 true, // no effect: every source is opened read-only
	"SubjectBinding.groupsSource":         true, // no reader
	"RelationshipPolicy.backend.type":     true, // no reader
	"RelationshipPolicy.backend.cacheTtl": true, // no reader: the TTL is policies.rebac.cacheTtl
}

// pages links each kind to the page that explains it.
var pages = map[apitypes.Kind]string{
	apitypes.KindSQLAccessPolicy:    "[Access control](../policies/access-control.md)",
	apitypes.KindRowFilterPolicy:    "[Row filters](../policies/row-filters.md)",
	apitypes.KindColumnMaskPolicy:   "[Column masks](../policies/column-masks.md)",
	apitypes.KindQueryRejectPolicy:  "[Rejects and rewrites](../policies/guardrails.md)",
	apitypes.KindQueryRewritePolicy: "[Rejects and rewrites](../policies/guardrails.md)",
	apitypes.KindApprovalPolicy:     "[Approvals](../policies/approvals.md)",
	apitypes.KindDataSource:         "[Data sources](../operations/data-sources.md)",
	apitypes.KindSubjectBinding:     "[Subjects](../policies/subjects.md)",
	apitypes.KindDataClassification: "[Classification](../policies/classification.md)",
	apitypes.KindRelationshipPolicy: "[ReBAC](../policies/rebac.md)",
}

// doc is the hand-written part of a row. {values} in text is replaced by
// the enum values of the field type.
type doc struct{ req, text string }

// docs is checked against pkg/apitypes/decoder.go, internal/policy and the
// readers of each field. The key is the table (object, shared, a type or a
// kind) plus the field path. A struct or list that holds other rows gets
// a row of its own only when it has an entry.
var docs = map[string]doc{
	"object.apiVersion":           {"yes", "{values}. See [Kinds](#kinds)."},
	"object.kind":                 {"yes", "See [Kinds](#kinds)."},
	"object.metadata.name":        {"yes", "Lower-case letters, digits and `-`. Starts and ends with a letter or digit. Unique per kind."},
	"object.metadata.labels":      {"", "Free keys and values. Sluice ignores them."},
	"object.metadata.annotations": {"", "Free keys and values. Sluice ignores them."},
	"object.metadata.priority":    {"", "0 to 1000. Higher comes first."},

	"shared.enforcementMode": {"", "{values}. Default `Enforce`. The others do not apply the policy."},
	"shared.match":           {"yes", "What the policy covers. Needs `any` or `all`."},
	"shared.exclude":         {"", "What the policy leaves out."},
	"shared.conditions":      {"", "All must be true."},

	"Selector.any": {"", "One clause must match."},
	"Selector.all": {"", "Every clause must match."},

	"Clause.subjects.jwtClaims[].claim":   {"yes", "Claim path. Dots separate the parts."},
	"Clause.subjects.jwtClaims[].op":      {"yes", "{values}."},
	"Clause.subjects.jwtClaims[].value":   {"", "For `Equals`, `NotEquals`."},
	"Clause.subjects.jwtClaims[].values":  {"", "For `In`, `NotIn`."},
	"Clause.subjects.jwtClaims[].pattern": {"", "Regular expression for `Matches`."},
	"Clause.subjects.groups":              {"", "Group names. One is enough."},
	"Clause.subjects.apiKeys":             {"", "Subject ids of API key subjects."},
	"Clause.subjects.roles":               {"", "Values of the `roles` claim. Without the claim, Sluice uses the groups."},
	"Clause.subjects.ipRanges":            {"", "CIDR ranges. The address of the client must be in one."},
	"Clause.resources":                    {"", "Tables to match."},

	"ResourceSelector.catalogs": {"", "Catalog names. `*` is a wildcard."},
	"ResourceSelector.schemas":  {"", "Schema names."},
	"ResourceSelector.tables":   {"", "Table names."},
	"ResourceSelector.columns":  {"", "Column names. A mask needs them."},
	"ResourceSelector.tags":     {"", "Tags of a `DataClassification`."},
	"ResourceSelector.actions":  {"", "{values}."},

	"Condition.name":       {"", "Name of the check."},
	"Condition.expression": {"yes", "CEL. Must give true or false."},

	"Predicate.all":    {"", "Every part must hold."},
	"Predicate.any":    {"", "One part must hold."},
	"Predicate.not":    {"", "The part must not hold."},
	"Predicate.column": {"", "Column to test."},
	"Predicate.op":     {"", "{values}."},
	"Predicate.value":  {"", "Value to compare with."},
	"Predicate.values": {"", "For `In`, `NotIn`, `Between`."},

	"SqlAccessPolicy.effect":    {"yes", "{values}."},
	"SqlAccessPolicy.message":   {"", "Message of a deny."},
	"SqlAccessPolicy.errorCode": {"", "Code of a deny. Default `ACL_DENIED`."},

	"RowFilterPolicy.filter.predicate":  {"one of two", "Filter as a tree."},
	"RowFilterPolicy.filter.expression": {"one of two", "Filter in CEL."},
	"RowFilterPolicy.combine":           {"", "{values}. Default `restrictive`: all filters must hold."},

	"ColumnMaskPolicy.mask.type":                {"yes", "{values}."},
	"ColumnMaskPolicy.mask.args.value":          {"", "`constant`: the new value."},
	"ColumnMaskPolicy.mask.args.showFirst":      {"", "`partial`: characters kept at the start."},
	"ColumnMaskPolicy.mask.args.showLast":       {"", "`partial`: characters kept at the end."},
	"ColumnMaskPolicy.mask.args.maskChar":       {"", "`partial`: replacement character. Default `*`."},
	"ColumnMaskPolicy.mask.args.algorithm":      {"", "`hash`: {values}. Default `sha256`."},
	"ColumnMaskPolicy.mask.args.saltRef":        {"", "`hash`: secret reference to the salt."},
	"ColumnMaskPolicy.mask.args.pattern":        {"", "`regex`: pattern to replace."},
	"ColumnMaskPolicy.mask.args.replacement":    {"", "`regex`: new text."},
	"ColumnMaskPolicy.mask.args.length":         {"", "`truncate`: characters kept."},
	"ColumnMaskPolicy.mask.args.suffix":         {"", "`truncate`: text added at the end."},
	"ColumnMaskPolicy.mask.args.range":          {"", "`jitter`: largest change. Above 0, below 1."},
	"ColumnMaskPolicy.mask.args.seed":           {"", "`jitter`, `fake`: seed. Plain text or a secret reference."},
	"ColumnMaskPolicy.mask.args.keyRef":         {"", "`fpe`: secret reference to the key."},
	"ColumnMaskPolicy.mask.args.tweak":          {"", "`fpe`: tweak in hex."},
	"ColumnMaskPolicy.mask.args.alphabet":       {"", "`fpe`: `numeric` (default), `lower` or `alphanumeric`."},
	"ColumnMaskPolicy.mask.args.customAlphabet": {"", "`fpe`: own alphabet."},
	"ColumnMaskPolicy.mask.args.fakeType":       {"", "`fake`: `name`, `first_name`, `last_name`, `email`, `phone`, `company`, `city`, `country` or `uuid`."},

	"QueryRejectPolicy.reject.rules":              {"yes", "At least one rule."},
	"QueryRejectPolicy.reject.rules[].name":       {"", "Rule name."},
	"QueryRejectPolicy.reject.rules[].expression": {"", "CEL. Empty means always reject."},
	"QueryRejectPolicy.reject.rules[].message":    {"", "Message for the client."},
	"QueryRejectPolicy.reject.rules[].code":       {"", "Error code. Default `ACL_REJECTED`."},

	"QueryRewritePolicy.rewrite":               {"yes", "At least one of the fields below."},
	"QueryRewritePolicy.rewrite.limit.max":     {"", "Maximum rows. 1 to 2147483647."},
	"QueryRewritePolicy.rewrite.sample.rate":   {"", "Share of rows. Above 0, at most 1."},
	"QueryRewritePolicy.rewrite.sample.method": {"", "{values}. Default `reservoir`."},
	"QueryRewritePolicy.rewrite.timeout":       {"", "Query timeout."},

	"ApprovalPolicy.when.columnsAccessed":     {"", "Column patterns. Use of one needs approval."},
	"ApprovalPolicy.when.predicates[].column": {"yes", "Column pattern in a comparison."},
	"ApprovalPolicy.when.predicates[].op":     {"", "`=`, `!=`, `<`, `<=`, `>`, `>=`, `like`, `ilike`, `in` or `isnull`. Empty means any."},
	"ApprovalPolicy.when.predicates[].value":  {"", "Compared value. Empty means any."},
	"ApprovalPolicy.reason":                   {"", "Text for the approver."},

	"DataSource.type":           {"yes", "{values}."},
	"DataSource.connection":     {"for `postgres`, `mysql`", "Connection URL."},
	"DataSource.credentialsRef": {"", "Secret reference. `postgres`, `mysql`: the password. `s3`: the access key as JSON."},
	"DataSource.path":           {"for `sqlite`, `duckdb_file`", "Database file."},
	"DataSource.bucket":         {"for `s3`", "Bucket name."},
	"DataSource.region":         {"for `s3`", "Region."},
	"DataSource.allowedPaths":   {"", "`s3`: files to read. One view per file."},
	"DataSource.endpoint":       {"", "`s3`: host of an S3-compatible store."},
	"DataSource.urls":           {"for `http`", "File URLs. One view per file."},
	"DataSource.headersRef":     {"", "`http`: secret reference to a JSON object of headers."},
	"DataSource.format":         {"", "`s3`, `http`: {values}. Default: the file extension."},
	"DataSource.database":       {"for `motherduck`", "Database name."},
	"DataSource.tokenRef":       {"for `motherduck`", "Secret reference to the token."},

	"SubjectBinding.issuer":                  {"", "Issuer (`iss`) of the JWTs."},
	"SubjectBinding.audience":                {"", "Expected audience (`aud`)."},
	"SubjectBinding.jwksUrl":                 {"", "URL of the signing keys. Needs `issuer` and `audience`."},
	"SubjectBinding.hmacSecretRef":           {"", "Secret reference to the HMAC key."},
	"SubjectBinding.jwksCacheTtl":            {"", "Cache time of the signing keys. Default `10m`."},
	"SubjectBinding.clockSkew":               {"", "Allowed clock difference. Default `60s`."},
	"SubjectBinding.claims.subjectId":        {"", "JWT: claim path, default `sub`. API key: the subject id."},
	"SubjectBinding.claims.email":            {"", "JWT: claim path. API key: the email."},
	"SubjectBinding.claims.groups":           {"", "JWT: claim path. API key: one group."},
	"SubjectBinding.claims.tenantId":         {"", "API key only: the `tenantId` claim."},
	"SubjectBinding.claims.allowedRegions":   {"", "API key only: the `allowedRegions` claim."},
	"SubjectBinding.rateLimit.rps":           {"", "Requests per second for each subject."},
	"SubjectBinding.rateLimit.burst":         {"", "Burst. Default 1."},
	"SubjectBinding.budget.cpuSecondsPerDay": {"", "Query seconds per subject and UTC day."},
	"SubjectBinding.budget.rowsPerDay":       {"", "Rows per subject and UTC day."},
	"SubjectBinding.apiKeys[].id":            {"yes", "Key id."},
	"SubjectBinding.apiKeys[].hashRef":       {"yes", "Secret reference to the key hash."},
	"SubjectBinding.apiKeys[].groups":        {"", "Groups of the subject."},

	"DataClassification.rules":             {"yes", "At least one rule."},
	"DataClassification.rules[].resources": {"", "Tables and columns. No `tags`, no `actions`. Without it, the rule tags every table."},
	"DataClassification.rules[].tags":      {"yes", "Tags to set. Lower-case letters, digits, `.`, `_`, `-`."},

	"RelationshipPolicy.backend.endpoint":             {"yes", "URL of the OpenFGA API."},
	"RelationshipPolicy.backend.storeId":              {"yes", "OpenFGA store id."},
	"RelationshipPolicy.backend.authorizationModelId": {"", "OpenFGA model id."},
	"RelationshipPolicy.backend.tokenRef":             {"", "Secret reference to the bearer token."},
	"RelationshipPolicy.backend.timeout":              {"", "Timeout of a check. Default `3s`."},
	"RelationshipPolicy.checks":                       {"yes", "At least one check."},
	"RelationshipPolicy.checks[].objectTemplate":      {"yes", "Object to check, for example `table:{{table}}`."},
	"RelationshipPolicy.checks[].relation":            {"yes", "Relation the subject needs."},
	"RelationshipPolicy.checks[].subjectTemplate":     {"", "Default `user:{{subject.id}}`."},
}

const header = `<!-- SPDX-License-Identifier: CC-BY-4.0 -->
<!-- Generated by scripts/gen-policy-docs.go. Do not edit by hand. -->

# Policy schema

A policy object is a YAML document with these fields.

`

const tableHead = "| Field | Type | Required | Meaning |\n|---|---|---|---|\n"

type kindInfo struct {
	kind    apitypes.Kind
	version apitypes.GroupVersion
	spec    reflect.Type
}

var (
	enums  = map[string][]string{}   // type name -> values, from the const blocks
	refs   = map[reflect.Type]int{}  // how often a struct type is used as a field
	order  []reflect.Type            // struct types in the order of first use
	shared = map[reflect.Type]bool{} // struct types that get a table of their own
	used   = map[string]bool{}       // skip and docs keys that matched
)

func main() {
	out := flag.String("out", "docs/reference/policy-schema.md", "output path")
	check := flag.Bool("check", false, "fail instead of write when output differs")
	flag.Parse()

	readEnums("pkg/apitypes")
	kinds := readKinds()

	// A top-level spec field that several kinds have is shown once, under
	// "Shared fields". A struct type that is used more than once gets a
	// table of its own.
	has := map[string][]apitypes.Kind{}
	for _, k := range kinds {
		for i := range k.spec.NumField() {
			name := yamlName(k.spec.Field(i))
			has[name] = append(has[name], k.kind)
		}
		countRefs(k.spec)
	}
	for _, t := range order {
		shared[t] = refs[t] > 1
	}
	isShared := func(f reflect.StructField) bool { return len(has[yamlName(f)]) > 1 }
	var sharedKinds []apitypes.Kind
	for _, k := range kinds {
		if slices.ContainsFunc(reflect.VisibleFields(k.spec), isShared) {
			sharedKinds = append(sharedKinds, k.kind)
		}
	}
	for name, with := range has {
		var missing []apitypes.Kind
		for _, k := range sharedKinds {
			if !slices.Contains(with, k) {
				missing = append(missing, k)
			}
		}
		if d, ok := docs["shared."+name]; ok && len(missing) > 0 {
			d.text += " Not in " + codeList(missing) + "."
			docs["shared."+name] = d
		}
	}

	var buf bytes.Buffer
	buf.WriteString(header + tableHead)
	rows(&buf, reflect.TypeFor[apitypes.TypeMeta](), "object", "", nil)
	rows(&buf, reflect.TypeFor[apitypes.ObjectMeta](), "object", "metadata.", nil)
	buf.WriteString("| `spec` | object | yes | The fields of the kind. |\n")

	buf.WriteString("\n## Kinds\n\n| Kind | `apiVersion` | Page |\n|---|---|---|\n")
	for _, k := range kinds {
		page, ok := pages[k.kind]
		if !ok {
			fatal("no page for kind %s: add it to pages, or to skip", k.kind)
		}
		fmt.Fprintf(&buf, "| [`%s`](#%s) | `%s` | %s |\n", k.kind, strings.ToLower(string(k.kind)), k.version, page)
	}

	buf.WriteString("\n## Shared fields\n\nThese kinds have the fields below: " + codeList(sharedKinds) + ".\n\n" + tableHead)
	seen := map[string]bool{}
	for _, k := range kinds {
		rows(&buf, k.spec, "shared", "", func(f reflect.StructField) bool {
			name := yamlName(f)
			first := isShared(f) && !seen[name]
			seen[name] = true
			return first
		})
	}

	buf.WriteString("\n## Types\n")
	for _, t := range order {
		if shared[t] {
			fmt.Fprintf(&buf, "\n### %s\n\n%s", t.Name(), tableHead)
			rows(&buf, t, t.Name(), "", nil)
		}
	}

	for _, k := range kinds {
		fmt.Fprintf(&buf, "\n## %s\n\n%s", k.kind, tableHead)
		rows(&buf, k.spec, string(k.kind), "", func(f reflect.StructField) bool { return !isShared(f) })
	}

	for key := range skip {
		if !used[key] {
			fatal("skip entry %q matches nothing: remove it", key)
		}
	}
	for key := range docs {
		if !used[key] {
			fatal("docs entry %q matches no field: remove it", key)
		}
	}

	abs, err := filepath.Abs(*out)
	if err != nil {
		fatal("resolve path: %v", err)
	}

	if *check {
		cur, err := os.ReadFile(abs)
		if err != nil {
			fatal("read existing: %v", err)
		}
		if !bytes.Equal(cur, buf.Bytes()) {
			fmt.Fprintln(os.Stderr, "policy-schema.md drift: run `make docs-generate`")
			os.Exit(1)
		}
		return
	}

	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		fatal("write %s: %v", abs, err)
	}
	fmt.Printf("wrote %s\n", abs)
}

// readEnums collects the typed string constants of the package:
// `Name Type = "value"` gives enums[Type] = [..., value].
func readEnums(dir string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			fatal("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				typ, ok := vs.Type.(*ast.Ident)
				if !ok || len(vs.Values) != 1 {
					continue
				}
				if lit, ok := vs.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					enums[typ.Name] = append(enums[typ.Name], v)
				}
			}
		}
	}
}

// readKinds looks up every Kind constant in the registry, which gives the
// apiVersion it is registered under and its Spec type.
func readKinds() []kindInfo {
	reg := apitypes.DefaultRegistry()
	var kinds []kindInfo
	for _, name := range enums["Kind"] {
		if skip[name] {
			used[name] = true
			continue
		}
		found := false
		for _, v := range enums["GroupVersion"] {
			tm := apitypes.TypeMeta{APIVersion: apitypes.GroupVersion(v), Kind: apitypes.Kind(name)}
			if obj, ok := reg.Lookup(tm); ok {
				spec, _ := reflect.TypeOf(obj).Elem().FieldByName("Spec")
				kinds = append(kinds, kindInfo{tm.Kind, tm.APIVersion, spec.Type})
				found = true
			}
		}
		if !found {
			fatal("kind %s is not in the registry: add it to skip", name)
		}
	}
	return kinds
}

// countRefs counts how often each struct type is used as a field type.
func countRefs(t reflect.Type) {
	for i := range t.NumField() {
		ft := elemType(t.Field(i).Type)
		if ft.Kind() != reflect.Struct {
			continue
		}
		refs[ft]++
		if refs[ft] == 1 {
			order = append(order, ft)
			countRefs(ft)
		}
	}
}

// rows writes one table row per field of t. keep filters the fields of t
// itself. A struct that is not shared is flattened into dotted paths.
func rows(buf *bytes.Buffer, t reflect.Type, scope, prefix string, keep func(reflect.StructField) bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		name := yamlName(f)
		if name == "" || keep != nil && !keep(f) {
			continue
		}
		path := prefix + name
		key := scope + "." + path
		if skip[key] {
			used[key] = true
			continue
		}
		elem := elemType(f.Type)
		nested := elem.Kind() == reflect.Struct && !shared[elem]
		if d, ok := docs[key]; ok {
			used[key] = true
			fmt.Fprintf(buf, "| `%s` | %s | %s | %s |\n", path, typeName(f.Type), d.req, expand(d.text, elem, key))
		} else if !nested {
			fatal("no docs entry for %s: add it to docs, or to skip", key)
		}
		if nested {
			if isList(f.Type) {
				path += "[]"
			}
			rows(buf, elem, scope, path+".", nil)
		}
	}
}

// expand replaces {values} with the enum values of the type t.
func expand(text string, t reflect.Type, key string) string {
	if !strings.Contains(text, "{values}") {
		return text
	}
	var vals []string
	for _, v := range enums[t.Name()] {
		if s := t.Name() + "=" + v; skip[s] {
			used[s] = true
			continue
		}
		vals = append(vals, v)
	}
	if len(vals) == 0 {
		fatal("%s: type %s has no enum values", key, t.Name())
	}
	return strings.ReplaceAll(text, "{values}", codeList(vals))
}

// codeList renders values as a comma-separated list of code spans.
func codeList[T ~string](vals []T) string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = "`" + string(v) + "`"
	}
	return strings.Join(out, ", ")
}

func isList(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Slice
}

// elemType strips pointers and slices.
func elemType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return t
}

// yamlName returns the YAML key of a field, or "" for an inline field.
func yamlName(f reflect.StructField) string {
	name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if opts == "inline" {
		return ""
	}
	return name
}

func typeName(t reflect.Type) string {
	elem := elemType(t)
	name := elem.Kind().String()
	switch {
	case elem == reflect.TypeFor[apitypes.Duration]():
		name = "duration"
	case shared[elem]:
		name = fmt.Sprintf("[%s](#%s)", elem.Name(), strings.ToLower(elem.Name()))
	case elem.Kind() == reflect.Struct:
		name = "object"
	case elem.Kind() == reflect.Interface:
		name = "any"
	case elem.Kind() == reflect.Int32, elem.Kind() == reflect.Int64:
		name = "int"
	case elem.Kind() == reflect.Float64:
		name = "float"
	}
	switch {
	case !isList(t):
		return name
	case shared[elem]:
		return name + " list"
	case elem.Kind() == reflect.Struct:
		return "list"
	}
	return "[]" + name
}

func fatal(f string, args ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
	os.Exit(1)
}
