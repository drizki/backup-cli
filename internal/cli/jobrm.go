package cli

import (
	"errors"
	"fmt"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

func jobCmd() *command {
	return &command{
		name:    "job",
		summary: "Manage job definitions",
		subs: []*command{{
			name:    "rm",
			summary: "Remove a job and its systemd timer; backups on disk are kept",
			usage:   "rm <job>",
			long: `Remove a job definition and its systemd service and timer.

Backups already written to the destination are never touched. To delete a
backup use 'backup-cli rm'.`,
			nargs: 1,
			setup: func(a *app, _ *flagSet) runFunc {
				return func(args []string) error { return a.runJobRm(args[0]) }
			},
		}},
	}
}

func (a *app) runJobRm(name string) error {
	if !job.ValidName(name) {
		return exitcode.Usagef("invalid job name %q", name)
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	j, err := store.LoadJob(name)
	if errors.Is(err, state.ErrNotFound) {
		return exitcode.With(exitcode.Usage, err)
	}
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}

	if j.Scheduled {
		if err := a.removeUnits(j.Name); err != nil {
			return err
		}
	}
	if err := store.RemoveJob(j.Name); err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	_, _ = fmt.Fprintf(a.out(), "removed job %s (backups under %s were left untouched)\n", j.Name, j.DstParent())
	return nil
}

// removeUnits disables the timer and deletes the unit files of a scheduled job.
func (a *app) removeUnits(name string) error {
	unit := systemd.UnitName(name)
	mgr := a.manager()
	if !mgr.Available() {
		_, _ = fmt.Fprintf(a.err(), "warning: systemd is not available; unit files for %s were not removed\n", name)
		return nil
	}
	if err := mgr.Disable(unit); err != nil {
		_, _ = fmt.Fprintf(a.err(), "warning: disable %s.timer: %v\n", unit, err)
	}
	if err := mgr.Remove(unit); err != nil {
		return exitcode.Failedf("remove units for %s (job file kept): %v", name, err)
	}
	return nil
}
