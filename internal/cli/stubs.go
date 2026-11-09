package cli

import (
	"fmt"

	"github.com/drizki/backup-cli/internal/exitcode"
)

// unavailable is the run function of commands whose engine is not linked in.
func unavailable(name string) runFunc {
	return func([]string) error {
		return exitcode.With(exitcode.Usage, fmt.Errorf("%s: %w", name, ErrUnavailable))
	}
}

func runCmd() *command {
	return &command{
		name: "run", summary: "Run a backup job now", usage: "run [flags] <job>", nargs: 1,
		setup: func(_ *app, fs *flagSet) runFunc {
			fs.Bool("dry-run", false, "plan only; write nothing anywhere")
			fs.Bool("from-source", false, "ignore the previous backup and read everything from the source")
			fs.Bool("force", false, "bypass the biweekly gate")
			fs.Int("jobs", 0, "parallel copy workers (default: the job's setting)")
			fs.Int("verify-sample", -1, "percent of files re-verified after commit (default: the job's setting, 0 disables)")
			return unavailable("run")
		},
	}
}

func tailCmd() *command {
	return &command{
		name: "tail", summary: "Follow a run's log until it finishes", usage: "tail <run-id>", nargs: 1,
		setup: func(*app, *flagSet) runFunc { return unavailable("tail") },
	}
}

func rmCmd() *command {
	return &command{
		name: "rm", summary: "Delete one backup (path or <job>/<name> id)", usage: "rm [flags] <backup>", nargs: 1,
		setup: func(_ *app, fs *flagSet) runFunc {
			fs.Bool("yes", false, "do not ask for confirmation")
			fs.Bool("dry-run", false, "show what would be deleted; delete nothing")
			fs.Bool("force", false, "allow deleting the last good backup of a job")
			return unavailable("rm")
		},
	}
}

func verifyCmd() *command {
	return &command{
		name: "verify", summary: "Re-hash a backup against its manifest", usage: "verify [flags] <backup>", nargs: 1,
		setup: func(_ *app, fs *flagSet) runFunc {
			fs.Float64("sample", 100, "percent of files to verify (0-100)")
			return unavailable("verify")
		},
	}
}

func doctorCmd() *command {
	return &command{
		name: "doctor", summary: "Check the environment, jobs and backups for problems", usage: "doctor [flags]", nargs: 0,
		setup: func(_ *app, fs *flagSet) runFunc {
			fs.Bool("json", false, "machine-readable JSON output")
			return unavailable("doctor")
		},
	}
}
