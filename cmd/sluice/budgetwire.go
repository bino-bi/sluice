// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/bino-bi/sluice/internal/budget"
	"github.com/bino-bi/sluice/internal/config"
)

// buildBudgetManager constructs the SQLite-backed budget manager when
// budget.enabled. Its fail posture mirrors audit.failClosed: a store that
// cannot be opened aborts startup when budget.failClosed (the default);
// with failClosed=false the error is logged and budgeting is disabled.
func buildBudgetManager(scfg *config.ServerConfig, snap *config.Snapshot, log *slog.Logger) (*budget.Manager, error) {
	if !scfg.Budget.Enabled {
		return nil, nil
	}
	dir := scfg.Budget.StateDir
	if dir == "" {
		dir = "./state"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		if scfg.Budget.FailClosed {
			return nil, fmt.Errorf("create budget state dir: %w", err)
		}
		log.Error("budget: cannot create state dir; budgeting disabled", slog.String("error", err.Error()))
		return nil, nil
	}
	mgr, err := budget.New(budget.Options{
		Path:          filepath.Join(dir, "budget.db"),
		FlushInterval: scfg.Budget.FlushInterval,
		RetentionDays: scfg.Budget.RetentionDays,
		Logger:        log,
	})
	if err != nil {
		if scfg.Budget.FailClosed {
			return nil, err
		}
		log.Error("budget: store unavailable; budgeting disabled", slog.String("error", err.Error()))
		return nil, nil
	}
	mgr.SetSpecs(buildBudgetSpecs(snap))
	return mgr, nil
}

// buildBudgetSpecs derives per-subject and per-issuer budget specs from
// SubjectBinding.spec.budget, mirroring buildRateSpecs.
func buildBudgetSpecs(snap *config.Snapshot) (bySubject, byIssuer map[string]budget.Spec) {
	bySubject = map[string]budget.Spec{}
	byIssuer = map[string]budget.Spec{}
	if snap == nil {
		return bySubject, byIssuer
	}
	for _, sb := range snap.SubjectBindings {
		if sb == nil || sb.Spec.Budget == nil {
			continue
		}
		spec := budget.Spec{
			CPUSecondsPerDay: sb.Spec.Budget.CPUSecondsPerDay,
			RowsPerDay:       sb.Spec.Budget.RowsPerDay,
		}
		if spec.CPUSecondsPerDay == 0 && spec.RowsPerDay == 0 {
			continue
		}
		if sb.Spec.Claims.SubjectID != "" {
			bySubject[sb.Spec.Claims.SubjectID] = spec
		}
		if sb.Spec.Issuer != "" {
			byIssuer[sb.Spec.Issuer] = spec
		}
	}
	return bySubject, byIssuer
}
