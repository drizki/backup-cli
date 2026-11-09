package cli

import (
	"github.com/drizki/backup-cli/internal/doctor"
	"github.com/drizki/backup-cli/internal/exitcode"
)

func doctorCmd() *command {
	return &command{
		name: "doctor", summary: "Check the environment, jobs and backups for problems", usage: "doctor [flags]", nargs: 0,
		long: "Reads only; never changes or deletes anything. Exits 0 when no check failed (warnings still exit 0), 1 when at least one failed.",
		setup: func(a *app, fs *flagSet) runFunc {
			asJSON := fs.Bool("json", false, "machine-readable JSON output")
			return func([]string) error { return a.doctor(*asJSON) }
		},
	}
}

func (a *app) doctor(asJSON bool) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	env := doctor.Env{Store: store, Manager: a.manager(), Version: versionString()}
	rep := doctor.Run(a.ctx, env)
	if asJSON {
		err = rep.WriteJSON(a.out())
	} else {
		err = rep.WriteText(a.out())
	}
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	if rep.Failed() {
		return exitcode.Failedf("%d check(s) failed", rep.Failures)
	}
	return nil
}
