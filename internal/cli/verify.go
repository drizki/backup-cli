package cli

import (
	"fmt"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/manifest"
)

func verifyCmd() *command {
	return &command{
		name: "verify", summary: "Re-hash a backup against its manifest", usage: "verify [flags] <backup>", nargs: 1,
		long: "<backup> is a directory or a <job>/<name> id. Exits 1 if anything is missing, altered or unreadable.",
		setup: func(a *app, fs *flagSet) runFunc {
			pct := fs.Float64("sample", 100, "percent of files to verify (above 0, at most 100)")
			return func(args []string) error { return a.verify(args[0], *pct) }
		},
	}
}

func (a *app) verify(arg string, pct float64) error {
	if pct <= 0 || pct > 100 {
		return exitcode.Usagef("--sample must be above 0 and at most 100, got %v", pct)
	}
	dir, err := a.resolveBackup(arg)
	if err != nil {
		return err
	}
	rep, err := manifest.Verify(a.ctx, dir, manifest.VerifyOptions{SamplePct: pct})
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	out := a.out()
	for _, w := range rep.Warnings {
		_, _ = fmt.Fprintf(out, "warning: %s\n", w)
	}
	for _, f := range rep.Failures {
		_, _ = fmt.Fprintf(out, "FAILED: %s\n", f)
	}
	if !rep.OK() {
		return exitcode.Failedf("verification of %s failed: %d problem(s)", dir, len(rep.Failures))
	}
	_, _ = fmt.Fprintf(out, "ok: %s (%d of %d file(s) re-hashed)\n", dir, rep.Checked, rep.Files)
	return nil
}
