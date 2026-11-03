// SPDX-License-Identifier: AGPL-3.0-or-later

package build

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bino-bi/sluice/internal/audit"
	"github.com/bino-bi/sluice/internal/datasource"
	"github.com/bino-bi/sluice/internal/executor"
	"github.com/bino-bi/sluice/internal/version"
	"github.com/bino-bi/sluice/pkg/apitypes"
	pkgds "github.com/bino-bi/sluice/pkg/datasource"
	pkgerr "github.com/bino-bi/sluice/pkg/errors"
)

// ErrNotFound is returned for a name that is not a synthetic data source.
var ErrNotFound = errors.New("no synthetic data source with this name")

// StateRunning is the state of a build that has not finished. A finished
// build has ResultSucceeded or ResultFailed.
const StateRunning = "running"

// Driver states, as the synthetic driver reports them. The driver
// imports this package, so it cannot be imported here.
const (
	stateNotBuilt = "not_built"
	stateStale    = "stale"
)

// keepBuilds is the number of finished builds kept per name.
const keepBuilds = 10

// Origins of a server build.
const (
	OriginAdmin   = "admin"
	OriginStartup = "startup"
)

// Auditor writes audit records. *audit.Dispatcher implements it.
type Auditor interface {
	Enqueue(ctx context.Context, r *audit.Record) error
}

// BuildView is one build of this server process.
type BuildView struct {
	BuildID    string    `json:"build_id"`
	State      string    `json:"state"`
	Origin     string    `json:"origin"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Error      string    `json:"error"`
}

// CurrentView names the build that current points to.
type CurrentView struct {
	BuildID    string    `json:"build_id"`
	SpecHash   string    `json:"spec_hash"`
	FinishedAt time.Time `json:"finished_at"`
}

// StatusView is the state of one synthetic data source. Current and
// Report are nil while it is not built.
type StatusView struct {
	Name     string       `json:"name"`
	Source   string       `json:"source"`
	Status   string       `json:"status"`
	SpecHash string       `json:"spec_hash"`
	Current  *CurrentView `json:"current"`
	Builds   []BuildView  `json:"builds"`
	Report   *Report      `json:"report"`
}

// ServiceOptions configures a Service.
type ServiceOptions struct {
	// DataSources is the snapshot the server started with. The drivers
	// hashed these specs, and a reload does not change data sources, so
	// a build of an edited spec would show stale at once.
	DataSources []*apitypes.DataSource
	// Tagger returns the tagger of the current policy snapshot, because
	// classifications change on reload. Nil means no tags.
	Tagger func() (Tagger, error)
	// Secrets resolves the seeds and the secrets of the sources.
	Secrets  pkgds.SecretResolver
	Dir      string
	MaxRatio float64
	Harden   executor.HardenConfig
	// Audit gets a start and an end record per build. FailClosed refuses
	// a build whose start record cannot be queued.
	Audit      Auditor
	FailClosed bool
	// Registry and Exec are the query pool. With both set, a good build
	// is attached in place of the old snapshot.
	Registry *datasource.Registry
	Exec     *executor.Executor
	// Schema drops the cached tables of a swapped catalog.
	Schema interface{ InvalidateCatalog(catalog string) }
	Log    *slog.Logger
	Clock  func() time.Time
}

// swapper is implemented by the synthetic driver.
type swapper interface {
	Swap(ctx context.Context, conn *sql.Conn, buildID, specHash string) error
}

// Service runs synthetic builds in the server, one at a time per name,
// in the background.
type Service struct {
	opts   ServiceOptions
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	running map[string]*job
	done    map[string][]BuildView
}

type job struct {
	view     BuildView
	remoteIP string
	// started closes once the start audit record is decided. startErr is
	// set before that when the record was refused and no build runs.
	started  chan struct{}
	startErr error
	finished chan struct{}
}

// NewService returns a Service. It logs every source whose snapshot was
// built from another spec and sets the stale and spike gauges.
func NewService(opts ServiceOptions) *Service {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		opts:    opts,
		ctx:     ctx,
		cancel:  cancel,
		running: map[string]*job{},
		done:    map[string][]BuildView{},
	}
	for _, ds := range opts.DataSources {
		if !isSynthetic(ds) {
			continue
		}
		name := ds.Metadata.Name
		state := s.state(name)
		setStale(name, state == stateStale)
		if state == stateStale {
			opts.Log.Warn("synthetic: the current snapshot was built from another spec; run a new build",
				"name", name)
		}
		spikes := 0
		if state != stateNotBuilt {
			if rep, err := s.currentReport(name); err == nil && rep != nil {
				spikes = len(rep.Spikes)
			}
		}
		setSpikes(name, spikes)
	}
	return s
}

func isSynthetic(ds *apitypes.DataSource) bool {
	return ds != nil && ds.Spec.Type == apitypes.DSSynthetic && ds.Spec.Synthetic != nil
}

// lookup returns the synthetic data source name and the source it reads.
func (s *Service) lookup(name string) (ds, src *apitypes.DataSource) {
	for _, d := range s.opts.DataSources {
		if d != nil && d.Metadata.Name == name {
			ds = d
		}
	}
	if !isSynthetic(ds) {
		return nil, nil
	}
	for _, d := range s.opts.DataSources {
		if d != nil && d.Metadata.Name == ds.Spec.Synthetic.Source {
			src = d
		}
	}
	if src == nil {
		return nil, nil
	}
	return ds, src
}

// state returns the driver state, or "" without a registry.
func (s *Service) state(name string) string {
	if s.opts.Registry == nil {
		return ""
	}
	d, err := s.opts.Registry.Lookup(name)
	if err != nil {
		return ""
	}
	if sr, ok := d.(pkgds.StateReporter); ok {
		return sr.State()
	}
	return ""
}

func (s *Service) currentReport(name string) (*Report, error) {
	l := Layout{Dir: s.opts.Dir, Name: name}
	id, err := l.ReadCurrent()
	if err != nil || id == "" {
		return nil, err
	}
	return l.ReadReport(id)
}

// Start starts a build of name. When a build of name runs, it returns
// that build and true. When the start audit record cannot be queued and
// FailClosed is set, it returns ERR_AUDIT_UNAVAILABLE and no build runs.
func (s *Service) Start(ctx context.Context, name, origin, remoteIP string) (BuildView, bool, error) {
	_, v, running, err := s.start(ctx, name, origin, remoteIP)
	return v, running, err
}

// start returns the job and a copy of its view taken before the build
// goroutine runs, so a new build always reports StateRunning.
func (s *Service) start(ctx context.Context, name, origin, remoteIP string) (*job, BuildView, bool, error) {
	ds, src := s.lookup(name)
	if ds == nil {
		return nil, BuildView{}, false, ErrNotFound
	}
	specHash, err := SpecHash(ds.Spec.Synthetic)
	if err != nil {
		return nil, BuildView{}, false, fmt.Errorf("synthetic %s: spec hash: %w", name, err)
	}
	now := s.opts.Clock()
	// A build that finished in the same second as this one would make
	// publish fail with ErrNewerCurrent.
	prev, _ := Layout{Dir: s.opts.Dir, Name: name}.ReadCurrent()
	id, err := newerBuildID(now, prev)
	if err != nil {
		return nil, BuildView{}, false, fmt.Errorf("synthetic %s: build id: %w", name, err)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, BuildView{}, false, fmt.Errorf("synthetic %s: the server is shutting down", name)
	}
	if j := s.running[name]; j != nil {
		s.mu.Unlock()
		// A refused start record removes the job, so do not report it
		// before the record is decided.
		select {
		case <-j.started:
		case <-ctx.Done():
			return nil, BuildView{}, false, ctx.Err()
		}
		if j.startErr != nil {
			return nil, BuildView{}, false, j.startErr
		}
		s.mu.Lock()
		v := j.view
		s.mu.Unlock()
		return j, v, true, nil
	}
	j := &job{
		view:     BuildView{BuildID: id, State: StateRunning, Origin: origin, StartedAt: now.UTC()},
		remoteIP: remoteIP,
		started:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	// Registered before the audit record, so a second Start meanwhile
	// gets this build and does not start another one.
	s.running[name] = j
	s.wg.Add(1)
	s.mu.Unlock()

	stub := &Report{
		BuildID:       id,
		Name:          name,
		Source:        ds.Spec.Synthetic.Source,
		Result:        StateRunning,
		Origin:        origin,
		SpecHash:      specHash,
		SluiceVersion: version.Current().Version,
	}
	if err := s.audit(ctx, stub, "start", origin, remoteIP, nil); err != nil {
		if s.opts.FailClosed {
			j.startErr = pkgerr.Wrap(pkgerr.CodeAuditUnavailable, err)
			s.mu.Lock()
			delete(s.running, name)
			s.mu.Unlock()
			s.wg.Done()
			close(j.started)
			close(j.finished)
			return nil, BuildView{}, false, j.startErr
		}
		s.opts.Log.Warn("synthetic: cannot write the start audit record", "name", name, "build_id", id, "error", err)
	}
	v := j.view
	close(j.started)
	go s.run(j, name, ds, src)
	return j, v, false, nil
}

// newerBuildID returns a new build id that sorts after prev. Ids have
// second resolution, so in the second of prev the id takes the next
// second.
func newerBuildID(now time.Time, prev string) (string, error) {
	id, err := newBuildID(now)
	if err != nil || id > prev {
		return id, err
	}
	t, perr := time.Parse("20060102T150405Z", prev[:16])
	if perr != nil {
		return id, nil
	}
	return newBuildID(t.Add(time.Second))
}

// audit writes the synthetic-build record of r for phase.
func (s *Service) audit(ctx context.Context, r *Report, phase, origin, remoteIP string, buildErr error) error {
	if s.opts.Audit == nil {
		return nil
	}
	rec := AuditRecord(r, phase)
	rec.Origin = origin
	rec.RemoteIP = remoteIP
	if origin == OriginAdmin {
		rec.Subject = audit.Subject{ID: "admin", Method: "admin-token", RemoteIP: remoteIP}
	} else {
		rec.Subject = audit.Subject{ID: "system"}
	}
	if errors.Is(buildErr, ErrCanceled) {
		rec.ErrorCode = string(pkgerr.CodeCanceled)
	}
	return s.opts.Audit.Enqueue(ctx, rec)
}

func (s *Service) run(j *job, name string, ds, src *apitypes.DataSource) {
	defer s.wg.Done()
	defer close(j.finished)
	origin := j.view.Origin
	start := time.Now()

	rep, err := s.build(j, ds, src)
	if err == nil {
		if serr := s.swap(name, rep); serr != nil {
			s.opts.Log.Error("synthetic: the build is published but cannot be attached",
				"name", name, "build_id", rep.BuildID, "error", serr)
			// The report file says succeeded: the snapshot is current and
			// is attached at the next start.
			failed := *rep
			failed.Result = ResultFailed
			failed.Error = "the build is published but cannot be attached; it is attached at the next start"
			rep, err = &failed, serr
		}
	}
	result := ResultSucceeded
	if err != nil {
		result = ResultFailed
		s.opts.Log.Error("synthetic: build failed", "name", name, "build_id", rep.BuildID, "origin", origin, "error", rep.Error)
	} else {
		s.opts.Log.Info("synthetic: build succeeded", "name", name, "build_id", rep.BuildID, "origin", origin)
	}
	observeBuild(result, time.Since(start))
	if err == nil {
		setSpikes(name, len(rep.Spikes))
	}
	setStale(name, s.state(name) == stateStale)

	// The service context may be canceled already. The dispatcher closes
	// after the service, so the end record still gets through.
	if aerr := s.audit(context.Background(), rep, "end", origin, j.remoteIP, err); aerr != nil {
		s.opts.Log.Warn("synthetic: cannot write the end audit record", "name", name, "build_id", rep.BuildID, "error", aerr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	j.view.State = result
	j.view.FinishedAt = rep.FinishedAt
	if j.view.FinishedAt.IsZero() {
		j.view.FinishedAt = s.opts.Clock().UTC()
	}
	j.view.Error = rep.Error
	delete(s.running, name)
	list := append([]BuildView{j.view}, s.done[name]...)
	if len(list) > keepBuilds {
		list = list[:keepBuilds]
	}
	s.done[name] = list
}

// build runs the build. It always returns a report: a build that fails
// before it has one gets a report that holds only the error.
func (s *Service) build(j *job, ds, src *apitypes.DataSource) (*Report, error) {
	in := Input{
		DataSource: ds,
		Source:     src,
		Secrets:    s.opts.Secrets,
		Dir:        s.opts.Dir,
		MaxRatio:   s.opts.MaxRatio,
		Harden:     s.opts.Harden,
		Origin:     j.view.Origin,
		BuildID:    j.view.BuildID,
		Clock:      s.opts.Clock,
		Log:        s.opts.Log,
	}
	var rep *Report
	var err error
	if s.opts.Tagger != nil {
		// A build without tags would skip strictTags, so no tagger means
		// no build.
		in.Tagger, err = s.opts.Tagger()
		if err != nil {
			err = fmt.Errorf("classifications: %w", err)
		}
	}
	if err == nil {
		rep, err = Run(s.ctx, in)
	}
	if rep == nil {
		now := s.opts.Clock()
		rep = &Report{
			BuildID:       j.view.BuildID,
			Name:          ds.Metadata.Name,
			Source:        ds.Spec.Synthetic.Source,
			Result:        ResultFailed,
			Error:         err.Error(),
			Origin:        j.view.Origin,
			SluiceVersion: version.Current().Version,
			StartedAt:     j.view.StartedAt,
			FinishedAt:    now.UTC(),
			DurationMs:    now.Sub(j.view.StartedAt).Milliseconds(),
		}
	}
	return rep, err
}

// swap attaches the new snapshot to the query pool (plan 1.4).
func (s *Service) swap(name string, rep *Report) error {
	if s.opts.Registry == nil || s.opts.Exec == nil {
		return nil
	}
	d, err := s.opts.Registry.Lookup(name)
	if err != nil {
		return err
	}
	sw, ok := d.(swapper)
	if !ok {
		return fmt.Errorf("synthetic %s: the driver cannot swap snapshots", name)
	}
	err = s.opts.Exec.Exclusive(s.ctx, func(ctx context.Context, conn *sql.Conn) error {
		return sw.Swap(ctx, conn, rep.BuildID, rep.SpecHash)
	})
	// Also after a failed swap: the fallback may have attached another
	// database.
	if s.opts.Schema != nil {
		s.opts.Schema.InvalidateCatalog(name)
	}
	// A health check that ran during the swap may have marked the
	// catalog down.
	_ = s.opts.Registry.Probe(s.ctx, name, datasource.NewSQLPool(s.opts.Exec.DB()))
	return err
}

// Status returns the state of name, the builds of this process (newest
// first) and the report of the current build.
func (s *Service) Status(name string) (StatusView, error) {
	ds, _ := s.lookup(name)
	if ds == nil {
		return StatusView{}, ErrNotFound
	}
	specHash, err := SpecHash(ds.Spec.Synthetic)
	if err != nil {
		return StatusView{}, fmt.Errorf("synthetic %s: spec hash: %w", name, err)
	}
	v := StatusView{
		Name:     name,
		Source:   ds.Spec.Synthetic.Source,
		Status:   s.state(name),
		SpecHash: specHash,
		Builds:   []BuildView{},
	}
	s.mu.Lock()
	if j := s.running[name]; j != nil {
		v.Builds = append(v.Builds, j.view)
	}
	v.Builds = append(v.Builds, s.done[name]...)
	s.mu.Unlock()

	if v.Status == stateNotBuilt {
		return v, nil
	}
	rep, err := s.currentReport(name)
	if err != nil {
		return StatusView{}, fmt.Errorf("synthetic %s: %w", name, err)
	}
	if rep != nil {
		v.Report = rep
		v.Current = &CurrentView{BuildID: rep.BuildID, SpecHash: rep.SpecHash, FinishedAt: rep.FinishedAt}
	}
	return v, nil
}

// StartMissing builds, one after another, every source with
// build.onStart ifMissing that is not built. It returns at once.
func (s *Service) StartMissing() {
	var names []string
	for _, ds := range s.opts.DataSources {
		if isSynthetic(ds) && ds.Spec.Synthetic.Build.OnStart == apitypes.BuildIfMissing &&
			s.state(ds.Metadata.Name) == stateNotBuilt {
			names = append(names, ds.Metadata.Name)
		}
	}
	if len(names) == 0 {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		for _, name := range names {
			j, _, _, err := s.start(s.ctx, name, OriginStartup, "")
			if err != nil {
				s.opts.Log.Error("synthetic: cannot start the start-time build", "name", name, "error", err)
				continue
			}
			select {
			case <-j.finished:
			case <-s.ctx.Done():
				return
			}
		}
	}()
}

// Close cancels running builds and waits for them up to ctx. A canceled
// build removes its temp files and ends as failed.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
