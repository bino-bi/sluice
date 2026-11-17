// SPDX-License-Identifier: AGPL-3.0-or-later

package layout

import (
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestNewBuildID(t *testing.T) {
	id, err := NewBuildID(time.Date(2026, 11, 2, 10, 15, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if !ValidBuildID(id) || id[:17] != "20261102T101500Z-" {
		t.Errorf("id = %q", id)
	}
}

func TestReadCurrent(t *testing.T) {
	l := Layout{Dir: t.TempDir(), Name: "crmsynth"}
	if err := l.Mkdirs(); err != nil {
		t.Fatal(err)
	}
	if id, err := l.ReadCurrent(); err != nil || id != "" {
		t.Fatalf("no file: %q, %v", id, err)
	}
	if err := os.WriteFile(l.current(), []byte("../x\n"), ReportMode); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadCurrent(); !errors.Is(err, ErrInvalidBuildID) {
		t.Errorf("bad id: err = %v", err)
	}
}

func TestReportSpecHash(t *testing.T) {
	l := Layout{Dir: t.TempDir(), Name: "crmsynth"}
	if err := l.Mkdirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReportSpecHash("../x"); !errors.Is(err, ErrInvalidBuildID) {
		t.Errorf("bad id: err = %v", err)
	}
	if _, err := l.ReportSpecHash(idCur); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("no report: err = %v", err)
	}
	if err := os.WriteFile(l.Report(idCur), []byte(`{"spec_hash":"sha256:ab"}`), ReportMode); err != nil {
		t.Fatal(err)
	}
	if h, err := l.ReportSpecHash(idCur); err != nil || h != "sha256:ab" {
		t.Errorf("hash = %q, %v", h, err)
	}
	if err := os.WriteFile(l.Report(idCur), []byte(`{`), ReportMode); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReportSpecHash(idCur); err == nil {
		t.Error("broken report: no error")
	}
}

// Publish keeps the current and the previous build, removes older
// finished builds and refuses to move current back.
func TestPublishRetainAndNewerCurrent(t *testing.T) {
	l := Layout{Dir: t.TempDir(), Name: "crmsynth"}
	if err := l.Mkdirs(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{idOld, idCur, idNew} {
		touch(t, l.Snapshot(id))
		touch(t, l.Report(id))
	}
	if err := l.Publish(idOld); err != nil {
		t.Fatal(err)
	}
	if err := l.Publish(idCur); err != nil {
		t.Fatal(err)
	}
	if err := l.Publish(idNew); err != nil {
		t.Fatal(err)
	}
	want := []string{idCur + ".duckdb", idNew + ".duckdb"}
	if got := snapshotNames(t, l); !slices.Equal(got, want) {
		t.Errorf("snapshots = %v, want %v", got, want)
	}
	if _, err := os.Stat(l.Report(idOld)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old report: %v", err)
	}
	if err := l.Publish(idCur); !errors.Is(err, ErrNewerCurrent) {
		t.Errorf("publish older: err = %v", err)
	}
	if id, _ := l.ReadCurrent(); id != idNew {
		t.Errorf("current = %q", id)
	}
}

// Fields that only change the report do not change the hash. Fields
// that change the data do.
func TestSpecHashLayout(t *testing.T) {
	if _, err := SpecHash(nil); err == nil {
		t.Error("nil spec: no error")
	}
	spec := func() *apitypes.SyntheticSpec {
		return &apitypes.SyntheticSpec{
			Source: "crm",
			Seed:   "s1",
			Tables: []apitypes.SyntheticTable{{
				Name:    "customers",
				Columns: map[string]apitypes.SyntheticColumn{"id": {}},
			}},
		}
	}
	base, err := SpecHash(spec())
	if err != nil {
		t.Fatal(err)
	}
	on := true
	s := spec()
	s.Build.OnStart = apitypes.BuildIfMissing
	s.Privacy = &apitypes.SyntheticPrivacy{SpikeWarning: &on, StrictTags: []string{"pii"}}
	s.Tables[0].Privacy = &apitypes.SyntheticPrivacy{SpikeWarning: &on}
	s.Tables[0].Columns["id"] = apitypes.SyntheticColumn{Privacy: &apitypes.SyntheticPrivacy{SpikeWarning: &on}}
	if h, _ := SpecHash(s); h != base {
		t.Errorf("report-only fields changed the hash")
	}
	n := 5
	s.Tables[0].Privacy.MinGroupSize = &n
	if h, _ := SpecHash(s); h == base {
		t.Errorf("minGroupSize did not change the hash")
	}
	s = spec()
	s.Seed = "s2"
	if h, _ := SpecHash(s); h == base {
		t.Errorf("seed did not change the hash")
	}
}
