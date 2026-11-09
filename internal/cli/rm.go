package cli

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/lock"
	"github.com/drizki/backup-cli/internal/safedelete"
	"github.com/drizki/backup-cli/internal/space"
	"github.com/drizki/backup-cli/internal/state"
)

type rmOptions struct {
	yes, dryRun, force bool
}

func rmCmd() *command {
	return &command{
		name: "rm", summary: "Delete one backup (path or <job>/<name> id)", usage: "rm [flags] <backup>", nargs: 1,
		long: `<backup> is a directory or a <job>/<name> id from 'backup-cli list backups'.

Every deletion passes the safedelete guards: a valid marker for the job, the
job's destination parent, no overlap with the source or the state directory,
no symlinks, no mount points. --force only lifts the last-good-backup rule;
it never bypasses a guard. A leftover <name>.partial directory can be removed
the same way. Without a terminal on stdin, --yes is required.`,
		setup: func(a *app, fs *flagSet) runFunc {
			var o rmOptions
			fs.BoolVar(&o.yes, "yes", false, "do not ask for confirmation")
			fs.BoolVar(&o.dryRun, "dry-run", false, "show what would be deleted; delete nothing")
			fs.BoolVar(&o.force, "force", false, "allow deleting the last good backup of a job")
			return func(args []string) error { return a.rm(args[0], o) }
		},
	}
}

func (a *app) rm(arg string, o rmOptions) error {
	if !o.dryRun && !o.yes && !a.isTTY() {
		return exitcode.Usagef("rm needs --yes when stdin is not a terminal (use --dry-run to preview)")
	}
	dir, err := a.resolveBackup(arg)
	if err != nil {
		return err
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return exitcode.With(exitcode.Usage, err)
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	j, partial, err := ownerJob(store, dir)
	if err != nil {
		return err
	}
	if !partial && !o.force {
		if err := lastGoodCheck(j, dir); err != nil {
			return err
		}
	}
	g := safedelete.Guard{StateDir: store.Dir}
	plan, err := planFor(g, j, dir, partial)
	if err != nil {
		return err
	}
	a.printPlan(plan)
	if o.dryRun {
		_, _ = fmt.Fprintln(a.out(), "dry run: nothing was deleted")
		return nil
	}
	return a.confirmAndRemove(store, g, j, plan, o)
}

func (a *app) confirmAndRemove(store state.Store, g safedelete.Guard, j job.Job, plan safedelete.Plan, o rmOptions) error {
	if !o.yes && !a.confirm() {
		_, _ = fmt.Fprintln(a.out(), "aborted: nothing was deleted")
		return nil
	}
	unlock, err := lockJob(store, j.Name)
	if err != nil {
		return err
	}
	defer unlock()
	// Another rm may have removed the sibling while this one waited at the
	// prompt, so the last-good rule is checked again under the lock.
	if !plan.Partial && !o.force {
		if err := lastGoodCheck(j, plan.Path); err != nil {
			return err
		}
	}
	return a.remove(g, j, plan)
}

// lockJob takes the job's run lock so rm cannot delete the partial backup of
// a running run or the backup it is seeding from. It refuses while a run
// holds the lock.
func lockJob(store state.Store, name string) (func(), error) {
	if err := store.Ensure(); err != nil {
		return nil, exitcode.With(exitcode.Failed, err)
	}
	l, err := lock.Acquire(store.LockPath(name))
	if errors.Is(err, lock.ErrHeld) {
		return nil, refusedf("a run of job %q is in progress; try again when it has finished", name)
	}
	if err != nil {
		return nil, exitcode.With(exitcode.Failed, err)
	}
	return func() { _ = l.Close() }, nil
}

// ownerJob finds the job that owns dir: the marker's job for a committed
// backup, the journal's job for a .partial directory.
func ownerJob(store state.Store, dir string) (j job.Job, partial bool, err error) {
	var name string
	if partial = strings.HasSuffix(dir, backupset.PartialSuffix); partial {
		rep, err := journal.Read(filepath.Join(dir, journal.Name))
		if err != nil {
			return j, partial, refusedf("%s has no readable journal, so its job is unknown: %v", dir, err)
		}
		name = rep.Job
	} else {
		m, err := backupset.ReadMarker(dir)
		if err != nil {
			return j, partial, refusedf("%s is not a committed backup (no valid %s marker): %v", dir, backupset.MarkerName, err)
		}
		name = m.Job
	}
	j, err = store.LoadJob(name)
	if errors.Is(err, state.ErrNotFound) {
		return j, partial, refusedf("%s belongs to job %q, which no longer exists, so the guards cannot be checked; remove it by hand if you are sure", dir, name)
	}
	if err != nil {
		return j, partial, exitcode.With(exitcode.Failed, err)
	}
	return j, partial, nil
}

func refusedf(format string, a ...any) error { return exitcode.Refusedf(format, a...) }

// lastGoodCheck refuses when dir is the only committed backup of the job.
func lastGoodCheck(j job.Job, dir string) error {
	all, err := backupset.Scan(j)
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	for _, b := range backupset.OnlyCommitted(all) {
		if b.Path != dir {
			return nil
		}
	}
	return refusedf("%s is the last good backup of job %q; pass --force to delete it anyway", dir, j.Name)
}

func planFor(g safedelete.Guard, j job.Job, dir string, partial bool) (safedelete.Plan, error) {
	var plan safedelete.Plan
	var err error
	if partial {
		plan, err = g.PlanPartial(j, dir)
	} else {
		plan, err = g.Plan(j, dir)
	}
	return plan, deleteError(err)
}

// deleteError maps a safedelete refusal to the refused exit code.
func deleteError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, safedelete.ErrRefused):
		return exitcode.With(exitcode.Refused, err)
	}
	return exitcode.With(exitcode.Failed, err)
}

func (a *app) printPlan(p safedelete.Plan) {
	kind := "backup"
	if p.Partial {
		kind = "partial backup"
	}
	_, _ = fmt.Fprintf(a.out(), "%s: %s\n  job:    %s\n", kind, p.Path, p.Job)
	if p.Partial {
		return
	}
	_, _ = fmt.Fprintf(a.out(), "  files:  %d\n  size:   %s\n", p.Files, space.Format(uint64(max(p.Bytes, 0))))
}

// confirm asks on the terminal; only "y" or "yes" confirms.
func (a *app) confirm() bool {
	_, _ = fmt.Fprint(a.out(), "Delete this backup permanently? [y/N] ")
	line, _ := bufio.NewReader(a.d.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func (a *app) isTTY() bool { return a.d.IsTTY != nil && a.d.IsTTY(a.d.Stdin) }

func (a *app) remove(g safedelete.Guard, j job.Job, p safedelete.Plan) error {
	var err error
	if p.Partial {
		err = g.DeletePartial(j, p.Path)
	} else {
		err = g.Delete(j, p.Path)
	}
	if err = deleteError(err); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.out(), "deleted %s\n", p.Path)
	return nil
}
