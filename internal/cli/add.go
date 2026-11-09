package cli

import (
	"fmt"
	"io"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

type addOpts struct {
	name         string
	schedule     string
	keep         int
	now          bool
	noSchedule   bool
	deadline     string
	onSuccess    string
	onFailure    string
	jobs         int
	verifySample int
}

const addLong = `Define a backup job that copies <source-dir> to a new directory per run.

<dst-template> is an absolute path whose last element contains {timestamp},
for example /mnt/backup/docs-{timestamp}. Every run creates a new full,
independent directory there.

Schedules:
  daily,HH:MM
  weekly,<day>,HH:MM
  biweekly,<day>,HH:MM       (runs weekly, skipped while the last backup is < 13 days old)
  monthly,<1-28>,HH:MM
  oncalendar:<systemd calendar expression>

Without systemd the job is refused and the equivalent cron line is printed;
use --no-schedule to store the job for manual or cron-driven runs.

Examples:
  backup-cli add /srv/data '/mnt/backup/data-{timestamp}' --schedule daily,02:30 --keep 7
  backup-cli add /srv/data '/mnt/backup/data-{timestamp}' --no-schedule --now`

func addCmd() *command {
	return &command{
		name:    "add",
		summary: "Define a backup job and schedule it",
		usage:   "add [flags] <source-dir> <dst-template>",
		long:    addLong,
		nargs:   2,
		setup: func(a *app, fs *flagSet) runFunc {
			var o addOpts
			fs.StringVar(&o.name, "name", "", "job name (default: slug of the source directory name)")
			fs.StringVar(&o.schedule, "schedule", "", "schedule spec (see above)")
			fs.IntVar(&o.keep, "keep", job.DefaultKeep, "number of committed backups to keep (>= 1)")
			fs.BoolVar(&o.now, "now", false, "start the first run immediately")
			fs.BoolVar(&o.noSchedule, "no-schedule", false, "store the job without installing a systemd timer")
			fs.StringVar(&o.deadline, "deadline", "", "stop runs at this time: RFC3339, \"YYYY-MM-DD HH:MM\" or a duration like 2h30m")
			fs.StringVar(&o.onSuccess, "on-success", "", "shell command to run after a successful run")
			fs.StringVar(&o.onFailure, "on-failure", "", "shell command to run after a failed or partial run")
			fs.IntVar(&o.jobs, "jobs", job.DefaultJobs, "parallel copy workers")
			fs.IntVar(&o.verifySample, "verify-sample", job.DefaultVerifySamplePct, "percent of files re-verified after commit (0 disables)")
			return func(args []string) error { return a.runAdd(args[0], args[1], o) }
		},
	}
}

func (a *app) runAdd(src, dst string, o addOpts) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	j, sched, err := a.buildJob(store, src, dst, o)
	if err != nil {
		return err
	}
	if err := j.CheckOverlap(); err != nil {
		return exitcode.Refusedf("refusing job: %v", err)
	}
	unit := systemd.Unit{Job: j.Name, StateDir: store.Dir, OnCalendar: j.OnCalendar}
	var mgr systemd.Manager
	if j.Scheduled {
		mgr = a.manager()
		if !mgr.Available() {
			return a.refuseNoSystemd(j, sched, store)
		}
		if unit.Binary, err = a.d.Executable(); err != nil {
			return exitcode.Failedf("locate own binary: %v", err)
		}
	}
	if err := saveJobAndUnits(store, j, mgr, unit); err != nil {
		return err
	}
	printAdded(a.out(), j, unit)
	if !o.now {
		return nil
	}
	return a.startNow(store, j, mgr, unit)
}

// saveJobAndUnits installs the timer (when scheduled) and writes the job
// file, rolling the timer back if anything fails.
func saveJobAndUnits(store state.Store, j job.Job, mgr systemd.Manager, unit systemd.Unit) error {
	if err := store.Ensure(); err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	if !j.Scheduled {
		return wrapSave(store.SaveJob(j))
	}
	if err := mgr.Install(unit); err != nil {
		return exitcode.Failedf("install units: %v", err)
	}
	if err := mgr.Enable(unit.Name()); err != nil {
		_ = mgr.Remove(unit.Name())
		return exitcode.Failedf("enable timer: %v", err)
	}
	if err := store.SaveJob(j); err != nil {
		_ = mgr.Disable(unit.Name())
		_ = mgr.Remove(unit.Name())
		return wrapSave(err)
	}
	return nil
}

func wrapSave(err error) error { return exitcode.With(exitcode.Failed, err) }

func printAdded(out io.Writer, j job.Job, unit systemd.Unit) {
	_, _ = fmt.Fprintf(out, "added job %s\n", j.Name)
	_, _ = fmt.Fprintf(out, "  source:      %s\n  destination: %s\n  keep:        %d\n", j.Source, j.DstTemplate, j.Keep)
	if !j.Scheduled {
		_, _ = fmt.Fprintf(out, "  schedule:    none (run manually with: backup-cli run %s)\n", j.Name)
		return
	}
	_, _ = fmt.Fprintf(out, "  schedule:    %s (OnCalendar=%s)\n  timer:       %s.timer enabled\n", j.Schedule, j.OnCalendar, unit.Name())
}
