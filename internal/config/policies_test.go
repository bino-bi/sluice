// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bino-bi/sluice/pkg/apitypes"
)

func TestLoadDirectory_EmptyIsValid(t *testing.T) {
	// Default-deny posture: an empty/missing directory yields a valid empty
	// Snapshot, not an error. Downstream packages interpret "no matching
	// policy" as deny.
	snap, err := LoadDirectory(context.Background(), LoadOptions{
		Sources: []SourceDir{{Path: t.TempDir()}},
	})
	if err != nil {
		t.Fatalf("empty dir should not error: %v", err)
	}
	if snap == nil {
		t.Fatal("snap should not be nil")
	}
	if len(snap.Policies) != 0 {
		t.Errorf("Policies = %d, want 0", len(snap.Policies))
	}
	if snap.ByKind == nil {
		t.Error("ByKind should be non-nil (even if empty)")
	}
}

func TestLoadDirectory_MissingDir_IsValid(t *testing.T) {
	snap, err := LoadDirectory(context.Background(), LoadOptions{
		Sources: []SourceDir{{Path: filepath.Join(t.TempDir(), "does-not-exist")}},
	})
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if snap == nil {
		t.Fatal("snap should not be nil")
	}
}

func TestLoadDirectory_ValidFixtures(t *testing.T) {
	snap, err := LoadDirectory(context.Background(), LoadOptions{
		Sources: []SourceDir{{Path: "testdata/policies/valid"}},
	})
	if err != nil {
		t.Fatalf("LoadDirectory: %v", err)
	}
	if len(snap.Policies) == 0 {
		t.Fatal("expected at least one policy from valid fixtures")
	}
	if snap.Digest == "" {
		t.Error("digest should be set for non-empty snapshot")
	}
	if got := len(snap.ByKind[apitypes.KindSQLAccessPolicy]); got == 0 {
		t.Errorf("ByKind[SqlAccessPolicy] empty; snap.ByKind=%v", snap.ByKind)
	}
}

func TestLoadDirectory_InvalidFixtures(t *testing.T) {
	_, err := LoadDirectory(context.Background(), LoadOptions{
		Sources: []SourceDir{{Path: "testdata/policies/invalid"}},
	})
	if err == nil {
		t.Fatal("expected validation error from invalid fixtures")
	}

	var verrs ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("want ValidationErrors, got %T: %v", err, err)
	}
	if len(verrs) == 0 {
		t.Fatal("ValidationErrors is empty")
	}

	found := false
	for _, ve := range verrs {
		if ve.File == "" {
			t.Errorf("ValidationError missing File: %+v", ve)
		}
		if ve.Msg == "" {
			t.Errorf("ValidationError missing Msg: %+v", ve)
		}
		if filepath.Base(ve.File) == "missing-name.yaml" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error citing missing-name.yaml; got %v", verrs)
	}
}

func TestLoadDirectory_DuplicateNames(t *testing.T) {
	dir := t.TempDir()
	doc := []byte(`apiVersion: sluice.bino.bi/v1alpha1
kind: SqlAccessPolicy
metadata:
  name: dupe
  priority: 100
spec:
  effect: allow
  match:
    any:
      - subjects:
          groups: ["x"]
`)
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), doc, 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.yaml"), doc, 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	_, err := LoadDirectory(context.Background(), LoadOptions{
		Sources: []SourceDir{{Path: dir}},
	})
	if err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestLoadDirectory_IgnoresNonYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap, err := LoadDirectory(context.Background(), LoadOptions{
		Sources: []SourceDir{{Path: dir}},
	})
	if err != nil {
		t.Fatalf("LoadDirectory: %v", err)
	}
	if len(snap.Policies) != 0 {
		t.Errorf("non-YAML files should be ignored; got %d policies", len(snap.Policies))
	}
}
