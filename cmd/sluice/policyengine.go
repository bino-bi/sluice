// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"

	"github.com/bino-bi/sluice/internal/config"
	"github.com/bino-bi/sluice/internal/opaengine"
	"github.com/bino-bi/sluice/internal/policy"
)

// buildPolicyEngine constructs the policy engine selected by
// policies.engine. It always returns the YAML engine (used for admin
// snapshot introspection and as a composite member) plus the engine the
// queryservice evaluates against (yaml or composite).
func buildPolicyEngine(scfg *config.ServerConfig, log *slog.Logger) (*policy.Engine, policy.PolicyEngine, error) {
	yamlEng := policy.New(policy.Options{Logger: log})

	switch scfg.Policies.Engine {
	case "", "yaml":
		return yamlEng, yamlEng, nil

	case "opa":
		opa, err := opaengine.New(opaengine.Options{
			ModuleDir: scfg.Policies.OPA.ModuleDir,
			Query:     scfg.Policies.OPA.Query,
			Logger:    log,
		})
		if err != nil {
			return nil, nil, err
		}
		return yamlEng, opa, nil

	case "composite":
		members, err := buildCompositeMembers(scfg, yamlEng, log)
		if err != nil {
			return nil, nil, err
		}
		return yamlEng, policy.NewComposite(policy.Options{Logger: log}, members...), nil

	default:
		return nil, nil, fmt.Errorf("unknown policies.engine %q (use yaml, opa, or composite)", scfg.Policies.Engine)
	}
}

// buildCompositeMembers resolves the configured member names into engines.
// Only the YAML engine is available today; OPA and ReBAC members land with
// their respective engines.
func buildCompositeMembers(scfg *config.ServerConfig, yamlEng *policy.Engine, log *slog.Logger) ([]policy.PolicyEngine, error) {
	names := scfg.Policies.Composite.Members
	if len(names) == 0 {
		names = []string{"yaml"}
	}
	members := make([]policy.PolicyEngine, 0, len(names))
	for _, name := range names {
		switch name {
		case "yaml":
			members = append(members, yamlEng)
		case "opa":
			opa, err := opaengine.New(opaengine.Options{
				ModuleDir: scfg.Policies.OPA.ModuleDir,
				Query:     scfg.Policies.OPA.Query,
				Logger:    log,
			})
			if err != nil {
				return nil, err
			}
			members = append(members, opa)
		default:
			return nil, fmt.Errorf("unknown composite member %q (use yaml or opa)", name)
		}
	}
	return members, nil
}
