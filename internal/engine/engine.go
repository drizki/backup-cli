// Package engine runs one backup job: lock, guards, plan, copy, commit,
// verify, retention and hooks. It never deletes anything itself; the only
// removal in a successful run is the retention callback supplied by the
// caller, and the run's own bookkeeping journal once the backup is complete.
package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/runlog"
	"github.com/drizki/backup-cli/internal/space"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/version"
)

// Engine holds the injectable seams of a run. The zero value is production.
type Engine struct {
	// OpenSource opens a source file by its path relative to the source root.
	OpenSource func(rel string) (*os.File, error)
	// OpenPrev opens a file of the previous backup by relative path.
	OpenPrev func(rel string) (*os.File, error)
	// AfterCopy runs after each source file's data is copied (tests mutate
	// the source here).
	AfterCopy func(rel string)
	// PostCommit runs after the atomic rename, before the sample verify.
	PostCommit func(dir string)
	// Statfs reports free space; nil means statfs(2).
	Statfs space.StatFunc
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
	// Retention prunes old backups after a fully successful run. The engine
	// itself never deletes; nil means no pruning.
	Retention func(ctx context.Context, j job.Job, committed []backupset.Backup, log *runlog.Log) error
	// HookTimeout bounds a hook; zero means five minutes.
	HookTimeout time.Duration
	// Rand selects the verification sample; nil seeds from the clock.
	Rand *rand.Rand
	// ProgressEvery is the status refresh interval; zero means five seconds.
	ProgressEvery time.Duration
}

// Run is the production entry point registered with the CLI.
func Run(ctx context.Context, req cli.EngineRequest) error {
	return (&Engine{}).Run(ctx, req)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// run is the state of one execution.
type run struct {
	e        *Engine
	req      cli.EngineRequest
	j        job.Job
	store    state.Store
	id       string
	start    time.Time
	deadline time.Time
	dry      bool
	log      *runlog.Log

	stMu sync.Mutex
	st   state.Status

	fromSource, fromPrev, srcBytes atomic.Int64
	backups                        []backupset.Backup
	finalDir                       string
}

// errSkipped ends a run early without doing anything (biweekly gate).
var errSkipped = errors.New("skipped")

// Run executes one job and returns an error carrying the exit code, or nil.
func (e *Engine) Run(ctx context.Context, req cli.EngineRequest) error {
	r := &run{e: e, req: req, j: req.Job, store: req.Store, dry: req.DryRun, start: e.now()}
	if r.j.Deadline != "" {
		d, err := job.ParseDeadline(r.j.Deadline, time.Now()) // context deadlines run on the wall clock
		if err != nil {
			return exitcode.With(exitcode.Usage, err)
		}
		r.deadline = d
	}
	r.id = state.NewRunID(r.start, r.j.Name)
	if err := r.open(); err != nil {
		return err
	}
	defer func() { _ = r.log.Close() }()
	stopTicker := r.startProgress()
	err := r.execute(ctx)
	stopTicker()
	return r.finish(ctx, err)
}

// open prepares the log and the initial status. Dry runs write nothing.
func (r *run) open() error {
	r.st = state.Status{
		RunID: r.id, Job: r.j.Name, State: state.StateRunning, Started: r.start.UTC(), PID: os.Getpid(),
		ToolVersion: version.Version, Source: r.j.Source, Dest: r.j.DstParent(),
		Warnings: []string{}, Errors: []string{}, DryRun: r.dry,
	}
	if r.dry {
		r.log = runlog.Mirror(r.req.Stdout)
		return nil
	}
	if err := r.store.Ensure(); err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	if err := os.MkdirAll(r.store.RunDir(r.id), 0o755); err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	r.st.Log = r.store.RunDir(r.id) + "/run.log"
	log, err := runlog.Open(r.st.Log, r.req.Stdout)
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	r.log = log
	r.update(nil)
	return nil
}

// update mutates the status under its lock and persists it (not on dry runs).
func (r *run) update(f func(*state.Status)) {
	r.stMu.Lock()
	defer r.stMu.Unlock()
	if f != nil {
		f(&r.st)
	}
	r.st.FilesCopiedFromSource = r.fromSource.Load()
	r.st.FilesCopiedFromPrev = r.fromPrev.Load()
	r.st.BytesFromSource = r.srcBytes.Load()
	if r.dry {
		return
	}
	if err := r.store.WriteStatus(r.st); err != nil {
		r.log.Warnf("could not write status: %v", err)
	}
}

func (r *run) warn(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	r.log.Warnf("%s", msg)
	r.update(func(s *state.Status) { s.Warnings = append(s.Warnings, msg) })
}

// startProgress refreshes the status file while the run is active.
func (r *run) startProgress() func() {
	if r.dry {
		return func() {}
	}
	every := r.e.ProgressEvery
	if every <= 0 {
		every = 5 * time.Second
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				r.update(nil)
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop); <-done }
}

// outcome maps an execution error to a final state and exit code.
func outcome(err error) (string, int) {
	switch {
	case err == nil:
		return state.StateSucceeded, exitcode.OK
	case errors.Is(err, errSkipped):
		return state.StateSkipped, exitcode.OK
	}
	switch code := exitcode.Code(err); code {
	case exitcode.Refused:
		return state.StateRefused, code
	case exitcode.Partial:
		return state.StatePartial, code
	default:
		return state.StateFailed, code
	}
}

// finish records the final status, fires the hook and returns the result.
func (r *run) finish(ctx context.Context, err error) error {
	err = r.stopped(err)
	st, code := outcome(err)
	r.update(func(s *state.Status) {
		s.State, s.ExitCode, s.Finished = st, code, r.e.now().UTC()
		if st == state.StateRefused {
			s.RefuseReason = err.Error()
		}
		if err != nil && !errors.Is(err, errSkipped) {
			s.Errors = append(s.Errors, err.Error())
		}
	})
	switch {
	case r.dry && st == state.StateSucceeded:
		r.log.Infof("dry run %s finished", r.id)
	case st == state.StateSucceeded:
		r.log.Infof("run %s finished: backup at %s", r.id, r.finalDir)
	case st == state.StateSkipped:
		r.log.Infof("run %s skipped: %s", r.id, r.st.SkipReason)
	default:
		r.log.Errorf("run %s %s: %v", r.id, st, err)
	}
	r.runHook(ctx, st, code)
	if errors.Is(err, errSkipped) {
		return nil
	}
	return err
}

// stopped converts cancellation errors into a partial-run result.
func (r *run) stopped(err error) error {
	if err == nil || exitcode.Code(err) != exitcode.Failed {
		return err
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	why := "interrupted"
	if errors.Is(err, context.DeadlineExceeded) {
		why = "deadline reached"
	}
	return exitcode.With(exitcode.Partial, fmt.Errorf("%s; the partial backup is kept and the next run resumes it", why))
}

func (r *run) workers() int {
	if r.req.Jobs > 0 {
		return r.req.Jobs
	}
	return max(r.j.Jobs, 1)
}
