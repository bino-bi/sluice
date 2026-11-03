// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// fakeAuditor records what it gets. With fail set it refuses every
// record. With gate set, the first record waits until the gate closes.
type fakeAuditor struct {
	mu      sync.Mutex
	records []*audit.Record
	fail    bool
	gate    chan struct{}
}

func (a *fakeAuditor) Enqueue(_ context.Context, r *audit.Record) error {
	a.mu.Lock()
	gate := a.gate
	a.gate = nil
	a.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if a.fail {
		return errors.New("queue full")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.records = append(a.records, r)
	return nil
}

func (a *fakeAuditor) all() []*audit.Record {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*audit.Record(nil), a.records...)
}

func newTestService(t *testing.T, aud Auditor, failClosed bool) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	spec := baseSpec()
	svc := NewService(ServiceOptions{
		DataSources: []*apitypes.DataSource{
			{
				Metadata: apitypes.ObjectMeta{Name: "crmsynth"},
				Spec:     apitypes.DataSourceSpec{Type: apitypes.DSSynthetic, Synthetic: spec},
			},
			{
				Metadata: apitypes.ObjectMeta{Name: "crm"},
				Spec:     apitypes.DataSourceSpec{Type: apitypes.DSDuckDBFile, Path: fixturePath(t, false)},
			},
		},
		Tagger:     func() (Tagger, error) { return mapTagger{"email": {"pii"}}, nil },
		Secrets:    mapSecrets{seedRef: []byte(testSeed)},
		Dir:        dir,
		Harden:     executor.HardenConfig{Threads: 4},
		Audit:      aud,
		FailClosed: failClosed,
		Log:        slog.New(slog.DiscardHandler),
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = svc.Close(ctx)
	})
	return svc, dir
}

func waitBuild(t *testing.T, svc *Service, name, id string) BuildView {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		v, err := svc.Status(name)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		for _, b := range v.Builds {
			if b.BuildID == id && b.State != StateRunning {
				return b
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("build %s did not finish", id)
	return BuildView{}
}

// Test 20: a server build writes a start and an end record.
func TestServiceAuditRecords(t *testing.T) {
	aud := &fakeAuditor{}
	svc, dir := newTestService(t, aud, true)
	v, running, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "203.0.113.9")
	if err != nil || running {
		t.Fatalf("start: %v, running %v", err, running)
	}
	if v.State != StateRunning || !ValidBuildID(v.BuildID) || v.Origin != OriginAdmin {
		t.Fatalf("view: %+v", v)
	}
	done := waitBuild(t, svc, "crmsynth", v.BuildID)
	if done.State != ResultSucceeded || done.Error != "" || done.FinishedAt.IsZero() {
		t.Fatalf("finished build: %+v", done)
	}

	recs := aud.all()
	if len(recs) != 2 {
		t.Fatalf("records: %d, want 2", len(recs))
	}
	for i, phase := range []string{"start", "end"} {
		r := recs[i]
		if r.EventType != audit.EventSyntheticBuild || r.Extras["phase"] != phase || r.Extras["build_id"] != v.BuildID {
			t.Errorf("%s record: %+v", phase, r)
		}
		if r.Origin != OriginAdmin || r.RemoteIP != "203.0.113.9" || r.Subject.ID != "admin" || r.Subject.Method != "admin-token" {
			t.Errorf("%s record subject: %+v, origin %q, ip %q", phase, r.Subject, r.Origin, r.RemoteIP)
		}
		if len(r.Catalogs) != 2 || r.Catalogs[0] != "crm" || r.Catalogs[1] != "crmsynth" {
			t.Errorf("%s catalogs: %v", phase, r.Catalogs)
		}
	}
	end := recs[1]
	if end.Decision != audit.DecisionAllow || end.Extras["result"] != ResultSucceeded {
		t.Errorf("end record: decision %s, extras %v", end.Decision, end.Extras)
	}
	if rows, _ := end.Extras["synthetic_rows"].(map[string]int64); rows["crm.customers"] != 4000 {
		t.Errorf("end record rows: %v", end.Extras["synthetic_rows"])
	}

	l := Layout{Dir: dir, Name: "crmsynth"}
	cur, err := l.ReadCurrent()
	if err != nil || cur != v.BuildID {
		t.Fatalf("current %q, %v; want %s", cur, err, v.BuildID)
	}
	rep, err := l.ReadReport(cur)
	if err != nil || rep.Origin != OriginAdmin || rep.Audit != nil {
		t.Fatalf("report: origin %q, audit %v, %v", rep.Origin, rep.Audit, err)
	}

	// Without a registry the state is unknown, so the status shows the
	// current build.
	st, err := svc.Status("crmsynth")
	if err != nil || st.Current == nil || st.Current.BuildID != cur || st.Report == nil || st.Source != "crm" {
		t.Fatalf("status: %+v, %v", st, err)
	}
	if _, err := svc.Status("crm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("status of a source that is not synthetic: %v", err)
	}
	if _, _, err := svc.Start(context.Background(), "nope", OriginAdmin, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("start of an unknown name: %v", err)
	}
}

// Test 20: with failClosed, a refused start record means no build.
func TestServiceFailClosed(t *testing.T) {
	svc, dir := newTestService(t, &fakeAuditor{fail: true}, true)
	_, _, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
	if ae := pkgerr.FromError(err); ae == nil || ae.Code != pkgerr.CodeAuditUnavailable {
		t.Fatalf("start: %v, want ERR_AUDIT_UNAVAILABLE", err)
	}
	st, err := svc.Status("crmsynth")
	if err != nil || len(st.Builds) != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
	if id, err := (Layout{Dir: dir, Name: "crmsynth"}).ReadCurrent(); err != nil || id != "" {
		t.Errorf("current: %q, %v", id, err)
	}
}

// Test 20: without failClosed the build runs although no record is
// written.
func TestServiceBestEffortAudit(t *testing.T) {
	svc, _ := newTestService(t, &fakeAuditor{fail: true}, false)
	v, _, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if b := waitBuild(t, svc, "crmsynth", v.BuildID); b.State != ResultSucceeded {
		t.Fatalf("build: %+v", b)
	}
}

// A second start while a build runs returns that build.
func TestServiceAlreadyRunning(t *testing.T) {
	gate := make(chan struct{})
	aud := &fakeAuditor{gate: gate}
	svc, _ := newTestService(t, aud, true)
	type result struct {
		v   BuildView
		err error
	}
	first := make(chan result, 1)
	go func() {
		v, _, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
		first <- result{v, err}
	}()
	// The first start waits in its start record, after it registered.
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := svc.Status("crmsynth")
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Builds) == 1 && st.Builds[0].State == StateRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first build is not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	type secondResult struct {
		v       BuildView
		running bool
		err     error
	}
	secondCh := make(chan secondResult, 1)
	go func() {
		v, running, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
		secondCh <- secondResult{v, running, err}
	}()
	// The second start waits until the start record of the first is
	// decided.
	select {
	case s := <-secondCh:
		t.Fatalf("second start returned before the start record: %+v", s)
	case <-time.After(100 * time.Millisecond):
	}
	close(gate)
	r := <-first
	second := <-secondCh
	if second.err != nil || !second.running {
		t.Fatalf("second start: running %v, %v", second.running, second.err)
	}
	if r.err != nil || r.v.BuildID != second.v.BuildID {
		t.Fatalf("first %+v (%v), second %+v", r.v, r.err, second.v)
	}
	waitBuild(t, svc, "crmsynth", r.v.BuildID)
}

// A start that overlaps a refused start record gets the same error, not
// a build that never runs.
func TestServiceAlreadyRunningFailClosed(t *testing.T) {
	gate := make(chan struct{})
	svc, _ := newTestService(t, &fakeAuditor{fail: true, gate: gate}, true)
	errs := make(chan error, 2)
	go func() {
		_, _, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
		errs <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := svc.Status("crmsynth")
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Builds) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first build is not registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	go func() {
		_, _, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
		errs <- err
	}()
	time.Sleep(50 * time.Millisecond)
	close(gate)
	for range 2 {
		if ae := pkgerr.FromError(<-errs); ae == nil || ae.Code != pkgerr.CodeAuditUnavailable {
			t.Fatalf("start: %v, want ERR_AUDIT_UNAVAILABLE", ae)
		}
	}
}

// A build that fails at once still starts as running.
func TestServiceStartReportsRunning(t *testing.T) {
	svc, _ := newTestService(t, &fakeAuditor{}, true)
	svc.opts.Tagger = func() (Tagger, error) { return nil, errors.New("no policy") }
	for range 20 {
		v, running, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, "")
		if err != nil || running {
			t.Fatalf("start: running %v, %v", running, err)
		}
		if v.State != StateRunning {
			t.Fatalf("state %q, want %q", v.State, StateRunning)
		}
		if b := waitBuild(t, svc, "crmsynth", v.BuildID); b.State != ResultFailed {
			t.Fatalf("build: %+v", b)
		}
	}
}

// Close cancels a running build: it ends as failed with ERR_CANCELED and
// leaves no temp file.
func TestServiceCloseCancels(t *testing.T) {
	aud := &fakeAuditor{}
	svc, dir := newTestService(t, aud, true)
	if _, _, err := svc.Start(context.Background(), "crmsynth", OriginStartup, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := svc.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	recs := aud.all()
	if len(recs) != 2 {
		t.Fatalf("records: %d, want 2", len(recs))
	}
	end := recs[1]
	if end.Subject.ID != "system" || end.Origin != OriginStartup {
		t.Errorf("end record subject %+v, origin %q", end.Subject, end.Origin)
	}
	if end.Extras["result"] == ResultSucceeded {
		t.Skip("the build finished before Close")
	}
	if end.ErrorCode != string(pkgerr.CodeCanceled) || end.Decision != audit.DecisionError {
		t.Errorf("end record: code %q, decision %s", end.ErrorCode, end.Decision)
	}
	assertNoTemp(t, Layout{Dir: dir, Name: "crmsynth"})
	if _, _, err := svc.Start(context.Background(), "crmsynth", OriginAdmin, ""); err == nil {
		t.Error("start after close succeeded")
	}
}

// Two server builds in one second: the second id must sort after the
// first, or publish fails with ErrNewerCurrent.
func TestNewerBuildID(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 15, 0, 0, time.UTC)
	for _, prev := range []string{"", "20261102T101500Z-ffffff", "20261102T101500Z-000000", "20261102T101459Z-ffffff"} {
		id, err := newerBuildID(now, prev)
		if err != nil || !ValidBuildID(id) || id <= prev {
			t.Errorf("newerBuildID(%q) = %q, %v", prev, id, err)
		}
	}
	if id, _ := newerBuildID(now, "20261102T101500Z-ffffff"); !strings.HasPrefix(id, "20261102T101501Z-") {
		t.Errorf("id after a build in the same second: %q", id)
	}
}
