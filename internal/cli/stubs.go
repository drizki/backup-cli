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

func doctorCmd() *command {
	return &command{
		name: "doctor", summary: "Check the environment, jobs and backups for problems", usage: "doctor [flags]", nargs: 0,
		setup: func(_ *app, fs *flagSet) runFunc {
			fs.Bool("json", false, "machine-readable JSON output")
			return unavailable("doctor")
		},
	}
}
