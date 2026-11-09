package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/exitcode"
)

// leaves lists every runnable command with its full path.
func leaves(cmds []*command, prefix []string) [][]string {
	var out [][]string
	for _, c := range cmds {
		path := append(append([]string(nil), prefix...), c.name)
		if len(c.subs) == 0 {
			out = append(out, path)
			continue
		}
		out = append(out, leaves(c.subs, path)...)
	}
	return out
}

// TestEveryCommandIsImplemented prints the help of every command and runs a
// smoke invocation of each one; a command that answers "not available" (or
// is missing from the smoke table) fails the test.
func TestEveryCommandIsImplemented(t *testing.T) {
	e := newRmEnv(t)
	e.deps.Engine = func(context.Context, EngineRequest) error { return nil }
	smoke := map[string][]string{
		"add":       {"add", e.src, e.tmpl("smoke"), "--name", "smoke", "--no-schedule"},
		"list jobs": {"list", "jobs"}, "list runs": {"list", "runs"}, "list backups": {"list", "backups"},
		"job rm":  {"job", "rm", "smoke"},
		"run":     {"run", "one"},
		"tail":    {"tail", "no-such-run"}, // reaches the command; a missing run is an ordinary failure
		"rm":      {"rm", "--dry-run", e.older},
		"verify":  {"verify", e.older},
		"doctor":  {"doctor"},
		"version": {"version"},
	}
	for _, path := range leaves(commands(), nil) {
		name := strings.Join(path, " ")
		args, ok := smoke[name]
		if !ok {
			t.Errorf("command %q has no smoke invocation in this test", name)
			continue
		}
		e.mustRun(append([]string{"help"}, path...)...)
		if !strings.Contains(e.out.String(), "Usage: backup-cli "+name) {
			t.Errorf("help for %q: %q", name, e.out.String())
		}
		e.run(args...)
		for _, text := range []string{ErrUnavailable.Error(), "not implemented", "not available"} {
			if strings.Contains(e.errb.String(), text) {
				t.Errorf("%q is not implemented: %s", name, e.errb.String())
			}
		}
	}
	e.expectCode(exitcode.Usage, "no-such-command")
}
