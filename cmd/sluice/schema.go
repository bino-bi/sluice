// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/spf13/cobra"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

// kindSchemas maps each supported Kind to the Go type that backs it. Kept in
// sync with apitypes.DefaultRegistry — adding a new Kind there requires an
// entry here so `sluice schema export` stays in lockstep with YAML decoding.
var kindSchemas = map[apitypes.Kind]reflect.Type{
	apitypes.KindSQLAccessPolicy:    reflect.TypeFor[apitypes.SQLAccessPolicy](),
	apitypes.KindRowFilterPolicy:    reflect.TypeFor[apitypes.RowFilterPolicy](),
	apitypes.KindColumnMaskPolicy:   reflect.TypeFor[apitypes.ColumnMaskPolicy](),
	apitypes.KindQueryRejectPolicy:  reflect.TypeFor[apitypes.QueryRejectPolicy](),
	apitypes.KindQueryRewritePolicy: reflect.TypeFor[apitypes.QueryRewritePolicy](),
	apitypes.KindApprovalPolicy:     reflect.TypeFor[apitypes.ApprovalPolicy](),
	apitypes.KindDataSource:         reflect.TypeFor[apitypes.DataSource](),
	apitypes.KindSubjectBinding:     reflect.TypeFor[apitypes.SubjectBinding](),
	apitypes.KindAuditSink:          reflect.TypeFor[apitypes.AuditSink](),
}

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Inspect the policy DSL schema",
	}
	cmd.AddCommand(newSchemaExportCmd())
	return cmd
}

func newSchemaExportCmd() *cobra.Command {
	var kindFilter string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export JSON Schema for the policy DSL",
		Long: `Export prints a JSON Schema document describing the sluice policy
YAML surface. The schema is suitable for IDEs (e.g. VS Code's yaml.schemas
setting) or CI linters.

Without --kind, the output is a top-level schema with a oneOf discriminator
across every registered Kind. With --kind, only that Kind's schema is emitted.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			if kindFilter != "" {
				t, ok := kindSchemas[apitypes.Kind(kindFilter)]
				if !ok {
					return &exitError{Code: 1, Msg: fmt.Sprintf("unknown kind %q (known: %s)", kindFilter, knownKinds())}
				}
				schema, err := jsonschema.ForType(t, nil)
				if err != nil {
					return &exitError{Code: 1, Err: fmt.Errorf("infer schema for %s: %w", kindFilter, err)}
				}
				return writeJSON(out, schema)
			}

			oneOf := make([]*jsonschema.Schema, 0, len(kindSchemas))
			for _, k := range sortedKinds() {
				s, err := jsonschema.ForType(kindSchemas[k], nil)
				if err != nil {
					return &exitError{Code: 1, Err: fmt.Errorf("infer schema for %s: %w", k, err)}
				}
				s.Title = string(k)
				oneOf = append(oneOf, s)
			}

			root := &jsonschema.Schema{
				Schema:      "https://json-schema.org/draft/2020-12/schema",
				ID:          "https://sluice.bino.bi/policy.schema.json",
				Title:       "Sluice Policy",
				Description: "Union schema covering every policy Kind accepted by sluice.",
				OneOf:       oneOf,
			}
			return writeJSON(out, root)
		},
	}

	cmd.Flags().StringVar(&kindFilter, "kind", "", "export only this Kind (e.g. SqlAccessPolicy)")
	return cmd
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return &exitError{Code: 1, Err: err}
	}
	return nil
}

func sortedKinds() []apitypes.Kind {
	ks := make([]apitypes.Kind, 0, len(kindSchemas))
	for k := range kindSchemas {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

func knownKinds() string {
	ks := sortedKinds()
	var sb strings.Builder
	for i, k := range ks {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(string(k))
	}
	return sb.String()
}
