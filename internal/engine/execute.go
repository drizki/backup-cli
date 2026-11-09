package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/lock"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/state"
)

// biweeklyGap is how young the newest backup must be for a biweekly job to skip.
const biweeklyGap = 13 * 24 * time.Hour

// execute runs the lifecycle after the status exists.
func (r *run) execute(ctx context.Context) error {
	mode := ""
	if r.dry {
		mode = " (dry run)"
	}
	r.log.Infof("run %s: job %s%s", r.id, r.j.Name, mode)
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if r.backups, err = backupset.Scan(r.j); err != nil {
		return exitcode.Failedf("scan backups: %v", err)
	}
	if r.gateSkips() {
		return errSkipped
	}
	cctx, cancel := r.deadlineCtx(ctx)
	defer cancel()
	p, err := r.prepare(cctx)
	if err != nil {
		return err
	}
	if r.dry {
		r.report(p)
		return nil
	}
	if err := r.copyAll(cctx, p); err != nil {
		return err
	}
	if err := r.commit(p); err != nil {
		return err
	}
	return r.afterCommit(ctx)
}

func (r *run) deadlineCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if r.deadline.IsZero() {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, r.deadline)
}

// lock takes the per-job lock; dry runs only check it.
func (r *run) lock() (func(), error) {
	path := r.store.LockPath(r.j.Name)
	if r.dry {
		held, err := lock.Held(path)
		if err != nil {
			return nil, exitcode.Failedf("check lock: %v", err)
		}
		if held {
			return nil, exitcode.Refusedf("another run of job %q is in progress", r.j.Name)
		}
		return func() {}, nil
	}
	l, err := lock.Acquire(path)
	if errors.Is(err, lock.ErrHeld) {
		return nil, exitcode.Refusedf("another run of job %q is in progress", r.j.Name)
	}
	if err != nil {
		return nil, exitcode.Failedf("lock: %v", err)
	}
	return func() { _ = l.Close() }, nil
}

// gateSkips implements the biweekly gate.
func (r *run) gateSkips() bool {
	if !r.j.Biweekly || r.req.Force {
		return false
	}
	committed := backupset.OnlyCommitted(r.backups)
	if len(committed) == 0 {
		return false
	}
	last := committed[len(committed)-1].Marker.Created
	age := r.e.now().Sub(last)
	if age < 0 {
		r.warn("clock skew: the last backup is dated %s in the future; treating it as old enough", (-age).Round(time.Minute))
		return false
	}
	if age >= biweeklyGap {
		return false
	}
	reason := "biweekly-gate"
	r.update(func(s *state.Status) { s.SkipReason = reason })
	r.log.Infof("biweekly gate: the last backup is %s old (needs %s); use --force to run anyway",
		age.Round(time.Minute), biweeklyGap)
	return true
}

func rootCollision(entries []manifest.Entry) error {
	for _, e := range entries {
		switch e.Path {
		case manifest.SHA256Name, manifest.TSVName, backupset.MarkerName, journal.Name:
			return exitcode.Refusedf("the source contains %q at its top level, which would collide with the backup's own files", e.Path)
		}
	}
	return nil
}

func describeSkipped(r *run, p *plan) {
	for _, s := range p.skipped {
		r.log.Warnf("skipped %s (%s)", s.Path, s.Kind)
	}
	if len(p.skipped) > 0 {
		r.update(func(st *state.Status) {
			st.Warnings = append(st.Warnings, fmt.Sprintf("%d special file(s) skipped", len(p.skipped)))
		})
	}
}
