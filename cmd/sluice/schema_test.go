// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"sigs.k8s.io/yaml"
)

func exportSchema(t *testing.T, args ...string) *jsonschema.Schema {
	t.Helper()
	root := newRootCmd()
	root.SetArgs(append([]string{"schema", "export"}, args...))
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	if err := root.Execute(); err != nil {
		t.Fatalf("schema export %v: %v (%s)", args, err, errOut.String())
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatalf("output is no JSON Schema: %v", err)
	}
	return &s
}

// The row filter predicate is a recursive type, which stopped the export of
// every kind. The exported schema must accept a nested filter and a
// duration written as text.
func TestSchemaExport(t *testing.T) {
	union := exportSchema(t)
	if len(union.OneOf) == 0 {
		t.Fatal("the union schema has no kinds")
	}
	if _, err := union.Resolve(nil); err != nil {
		t.Fatalf("union schema does not resolve: %v", err)
	}

	filter, err := exportSchema(t, "--kind", "RowFilterPolicy").Resolve(nil)
	if err != nil {
		t.Fatalf("RowFilterPolicy schema does not resolve: %v", err)
	}
	doc := map[string]any{
		"apiVersion": "sluice.bino.bi/v1alpha1",
		"kind":       "RowFilterPolicy",
		"metadata":   map[string]any{"name": "tenant"},
		"spec": map[string]any{
			"match": map[string]any{},
			"filter": map[string]any{"predicate": map[string]any{
				"all": []any{
					map[string]any{"column": "tenant_id", "op": "Equals", "value": "acme"},
					map[string]any{"not": map[string]any{"any": []any{
						map[string]any{"column": "region", "op": "IsNull"},
					}}},
				},
			}},
		},
	}
	if err := filter.Validate(doc); err != nil {
		t.Errorf("a nested predicate does not validate: %v", err)
	}
	doc["spec"].(map[string]any)["filter"].(map[string]any)["predicate"] = map[string]any{"all": "not a list"}
	if err := filter.Validate(doc); err == nil {
		t.Error("a predicate with a wrong type validates")
	}

	rewrite, err := exportSchema(t, "--kind", "QueryRewritePolicy").Resolve(nil)
	if err != nil {
		t.Fatalf("QueryRewritePolicy schema does not resolve: %v", err)
	}
	for _, timeout := range []any{"10s", 10} {
		doc := map[string]any{
			"apiVersion": "sluice.bino.bi/v1alpha1",
			"kind":       "QueryRewritePolicy",
			"metadata":   map[string]any{"name": "limit"},
			"spec": map[string]any{
				"match":   map[string]any{},
				"rewrite": map[string]any{"timeout": timeout},
			},
		}
		if err := rewrite.Validate(doc); err != nil {
			t.Errorf("timeout %v does not validate: %v", timeout, err)
		}
	}
}

// Every manifest of the examples loads in Sluice, so it must validate
// against the exported schema of its kind. A field that the Go type marks
// as required while the decoder takes it as optional would fail here.
func TestSchemaExport_AcceptsTheExampleManifests(t *testing.T) {
	files, err := filepath.Glob("../../examples/*/policies.d/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no example manifests found: %v", err)
	}
	schemas := map[string]*jsonschema.Resolved{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, doc := range strings.Split(string(raw), "\n---") {
			var obj map[string]any
			if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
				t.Fatalf("%s document %d: %v", file, i+1, err)
			}
			kind, _ := obj["kind"].(string)
			if kind == "" {
				continue
			}
			if schemas[kind] == nil {
				if schemas[kind], err = exportSchema(t, "--kind", kind).Resolve(nil); err != nil {
					t.Fatalf("%s schema does not resolve: %v", kind, err)
				}
			}
			if err := schemas[kind].Validate(obj); err != nil {
				t.Errorf("%s document %d (%s): %v", file, i+1, kind, err)
			}
		}
	}
}
