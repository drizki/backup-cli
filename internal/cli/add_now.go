package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/schedule"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

func (a *app) startNow(store state.Store, j job.Job, mgr systemd.Manager, unit systemd.Unit) error {
	out := a.out()
	if j.Scheduled && mgr != nil && mgr.Available() {
		if err := mgr.Start(unit.Name()); err != nil {
			return exitcode.Failedf("start %s.service: %v", unit.Name(), err)
		}
		_, _ = fmt.Fprintf(out, "started %s.service in the background\n", unit.Name())
		_, _ = fmt.Fprintf(out, "find its run id:  backup-cli --state-dir %s list runs --job %s\n", shellQuote(store.Dir), j.Name)
		_, _ = fmt.Fprintf(out, "follow the log:   backup-cli --state-dir %s tail <run-id>\n", shellQuote(store.Dir))
		return nil
	}
	if a.d.Engine == nil {
		return exitcode.With(exitcode.Usage, fmt.Errorf("run: %w", ErrUnavailable))
	}
	_, _ = fmt.Fprintf(out, "running %s in the foreground\n", j.Name)
	return a.d.Engine(a.ctx, EngineRequest{
		Store: store, Job: j, VerifySample: -1,
		Stdout: out, Stderr: a.err(),
	})
}

// refuseNoSystemd prints the cron equivalent and refuses (exit 4). Nothing is
// saved, installed or enabled.
func (a *app) refuseNoSystemd(j job.Job, s schedule.Result, store state.Store) error {
	out := a.out()
	if s.Cron == "" {
		_, _ = fmt.Fprintf(out, "systemd is not available and a cron equivalent cannot be derived for %q.\n", j.Schedule)
	} else {
		exe, err := a.d.Executable()
		if err != nil || exe == "" {
			exe = "/path/to/backup-cli"
		}
		_, _ = fmt.Fprintln(out, "systemd is not available. Equivalent cron line:")
		if j.Biweekly {
			_, _ = fmt.Fprintln(out, "# runs weekly; backup-cli skips a run while the last backup is younger than 13 days")
		}
		_, _ = fmt.Fprintf(out, "%s %s --state-dir %s run %s\n", s.Cron, shellQuote(exe), shellQuote(store.Dir), j.Name)
	}
	return exitcode.Refusedf("systemd is not available: job %q was not saved or scheduled (use --no-schedule to store it for manual or cron-driven runs)", j.Name)
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell only when needed.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
