// SPDX-License-Identifier: AGPL-3.0-or-later

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/synth/build"
	"github.com/bino-bi/sluice/internal/transport/admin"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

const testBuildID = "20261102T101500Z-a1b2c3"

type fakeSynth struct {
	running  bool
	startErr error
	gotName  string
	gotIP    string
}

func (f *fakeSynth) Start(_ context.Context, name, remoteIP string) (build.BuildView, bool, error) {
	f.gotName, f.gotIP = name, remoteIP
	if name != "crmsynth" {
		return build.BuildView{}, false, build.ErrNotFound
	}
	if f.startErr != nil {
		return build.BuildView{}, false, f.startErr
	}
	return build.BuildView{BuildID: testBuildID, State: build.StateRunning, Origin: "admin"}, f.running, nil
}

func (f *fakeSynth) Status(name string) (build.StatusView, error) {
	if name != "crmsynth" {
		return build.StatusView{}, build.ErrNotFound
	}
	fin := time.Date(2026, 11, 2, 10, 15, 30, 0, time.UTC)
	return build.StatusView{
		Name:     "crmsynth",
		Source:   "crm",
		Status:   "ready",
		SpecHash: "sha256:ab",
		Current:  &build.CurrentView{BuildID: testBuildID, SpecHash: "sha256:ab", FinishedAt: fin},
		Builds:   []build.BuildView{{BuildID: testBuildID, State: build.ResultSucceeded, Origin: "admin", FinishedAt: fin}},
		Report:   &build.Report{BuildID: testBuildID, Result: build.ResultSucceeded},
	}, nil
}

func serveSynth(t *testing.T, synth admin.SynthService, token, method, path, auth string) *httptest.ResponseRecorder {
	t.Helper()
	srv := admin.New(admin.Config{Listen: ":0", Enabled: true, Token: token}, admin.Deps{Synth: synth})
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = "203.0.113.5:4000"
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

func TestSynth_NotEnabled501(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/admin/synthetic/crmsynth/build"},
		{http.MethodGet, "/admin/synthetic/crmsynth"},
	} {
		if w := serveSynth(t, nil, "", tc.method, tc.path, ""); w.Code != http.StatusNotImplemented {
			t.Errorf("%s %s: status %d, want 501", tc.method, tc.path, w.Code)
		}
	}
}

func TestSynth_Build202(t *testing.T) {
	t.Parallel()
	for _, running := range []bool{false, true} {
		f := &fakeSynth{running: running}
		w := serveSynth(t, f, "", http.MethodPost, "/admin/synthetic/crmsynth/build", "")
		if w.Code != http.StatusAccepted {
			t.Fatalf("status %d, want 202: %s", w.Code, w.Body)
		}
		if loc := w.Header().Get("Location"); loc != "/admin/synthetic/crmsynth" {
			t.Errorf("Location = %q", loc)
		}
		var body map[string]any
		if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["name"] != "crmsynth" || body["build_id"] != testBuildID || body["state"] != "running" || body["already_running"] != running {
			t.Errorf("body: %v", body)
		}
		if f.gotName != "crmsynth" || f.gotIP != "203.0.113.5" {
			t.Errorf("start got name %q, ip %q", f.gotName, f.gotIP)
		}
	}
}

func TestSynth_UnknownName404(t *testing.T) {
	t.Parallel()
	f := &fakeSynth{}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/admin/synthetic/crm/build"},
		{http.MethodGet, "/admin/synthetic/crm"},
	} {
		w := serveSynth(t, f, "", tc.method, tc.path, "")
		if w.Code != http.StatusNotFound || w.Body.Len() != 0 {
			t.Errorf("%s %s: status %d, body %q; want 404 and no body", tc.method, tc.path, w.Code, w.Body)
		}
	}
}

func TestSynth_AuditUnavailable503(t *testing.T) {
	t.Parallel()
	f := &fakeSynth{startErr: pkgerr.Wrap(pkgerr.CodeAuditUnavailable, errors.New("queue full"))}
	w := serveSynth(t, f, "", http.MethodPost, "/admin/synthetic/crmsynth/build", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil || body["code"] != "ERR_AUDIT_UNAVAILABLE" {
		t.Errorf("body %v, %v", body, err)
	}
}

func TestSynth_StatusShape(t *testing.T) {
	t.Parallel()
	w := serveSynth(t, &fakeSynth{}, "", http.MethodGet, "/admin/synthetic/crmsynth", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "source", "status", "spec_hash", "current", "builds", "report"} {
		if _, ok := body[k]; !ok {
			t.Errorf("key %q missing: %v", k, body)
		}
	}
	cur, _ := body["current"].(map[string]any)
	if cur["build_id"] != testBuildID || cur["finished_at"] != "2026-11-02T10:15:30Z" {
		t.Errorf("current: %v", cur)
	}
	builds, _ := body["builds"].([]any)
	if len(builds) != 1 {
		t.Fatalf("builds: %v", body["builds"])
	}
	b := builds[0].(map[string]any)
	for _, k := range []string{"build_id", "state", "origin", "started_at", "finished_at", "error"} {
		if _, ok := b[k]; !ok {
			t.Errorf("build key %q missing: %v", k, b)
		}
	}
}

func TestSynth_AuthRequired(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/admin/synthetic/crmsynth/build"},
		{http.MethodGet, "/admin/synthetic/crmsynth"},
	} {
		f := &fakeSynth{}
		if w := serveSynth(t, f, "secret", tc.method, tc.path, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token: %d", tc.method, tc.path, w.Code)
		}
		if f.gotName != "" {
			t.Errorf("%s %s: the build started without a token", tc.method, tc.path)
		}
		if w := serveSynth(t, f, "secret", tc.method, tc.path, "secret"); w.Code/100 != 2 {
			t.Errorf("%s %s with token: %d", tc.method, tc.path, w.Code)
		}
	}
}
