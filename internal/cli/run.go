package cli

import (
	"errors"
	"fmt"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/state"
)

func runCmd() *command {
	return &command{
		name: "run", summary: "Run a backup job now", usage: "run [flags] <job>", nargs: 1,
		long: "Exit codes: 0 succeeded or skipped, 1 failed, 3 partial (deadline or interruption; the next run resumes), 4 refused.",
		setup: func(a *app, fs *flagSet) runFunc {
			req := EngineRequest{}
			fs.BoolVar(&req.DryRun, "dry-run", false, "plan only; write nothing anywhere")
			fs.BoolVar(&req.FromSource, "from-source", false, "ignore the previous backup and read everything from the source")
			fs.BoolVar(&req.Force, "force", false, "bypass the biweekly gate")
			fs.IntVar(&req.Jobs, "jobs", 0, "parallel copy workers (default: the job's setting)")
			fs.IntVar(&req.VerifySample, "verify-sample", -1, "percent of files re-verified after commit (default: the job's setting, 0 disables)")
			return func(args []string) error { return a.runJob(args[0], req) }
		},
	}
}

func (a *app) runJob(name string, req EngineRequest) error {
	if req.Jobs < 0 || req.VerifySample < -1 || req.VerifySample > 100 {
		return exitcode.Usagef("--jobs must be >= 0 and --verify-sample between 0 and 100")
	}
	store, err := a.store()
	if err != nil {
		return err
	}
	j, err := store.LoadJob(name)
	if errors.Is(err, state.ErrNotFound) {
		return exitcode.With(exitcode.Usage, fmt.Errorf("no such job %q (see 'backup-cli list jobs')", name))
	}
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	if a.d.Engine == nil {
		return exitcode.With(exitcode.Usage, fmt.Errorf("run: %w", ErrUnavailable))
	}
	req.Store, req.Job, req.Stdout, req.Stderr = store, j, a.out(), a.err()
	return a.d.Engine(a.ctx, req)
}
