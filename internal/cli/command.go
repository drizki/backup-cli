package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/version"
)

type flagSet = flag.FlagSet

type runFunc func(args []string) error

// command is one entry of the command table. Groups (list, job) carry subs
// and no setup.
type command struct {
	name    string
	summary string
	usage   string // synopsis after "backup-cli "
	long    string
	nargs   int // exact positional arguments required
	subs    []*command
	setup   func(a *app, fs *flagSet) runFunc
}

func versionString() string { return version.String() }

func commands() []*command {
	return []*command{
		addCmd(), listCmd(), jobCmd(), runCmd(), tailCmd(), rmCmd(), verifyCmd(), doctorCmd(), versionCmd(),
	}
}

func find(cmds []*command, name string) *command {
	for _, c := range cmds {
		if c.name == name {
			return c
		}
	}
	return nil
}

func isHelp(s string) bool { return s == "-h" || s == "--help" || s == "-help" || s == "help" }

// dispatch resolves args against cmds. prefix is the already-matched path.
func (a *app) dispatch(cmds []*command, prefix string, args []string) error {
	if isHelp(args[0]) {
		return a.help(cmds, prefix, args[1:])
	}
	c := find(cmds, args[0])
	if c == nil {
		return exitcode.Usagef("unknown command %q", strings.TrimSpace(prefix+args[0]))
	}
	if len(c.subs) == 0 {
		return a.execute(c, prefix, args[1:])
	}
	if len(args) == 1 {
		return exitcode.Usagef("missing subcommand\n\n%s", groupHelp(c, prefix))
	}
	return a.dispatch(c.subs, prefix+c.name+" ", args[1:])
}

// help prints top-level help, or help for the command path in rest.
func (a *app) help(cmds []*command, prefix string, rest []string) error {
	if len(rest) == 0 {
		_, err := io.WriteString(a.out(), rootHelpFor(cmds, prefix))
		return err
	}
	c := find(cmds, rest[0])
	if c == nil {
		return exitcode.Usagef("unknown command %q", strings.TrimSpace(prefix+rest[0]))
	}
	if len(c.subs) > 0 {
		if len(rest) > 1 {
			return a.help(c.subs, prefix+c.name+" ", rest[1:])
		}
		_, err := io.WriteString(a.out(), groupHelp(c, prefix))
		return err
	}
	_, err := io.WriteString(a.out(), commandHelp(a, c, prefix))
	return err
}

func rootHelp(cmds []*command) string { return rootHelpFor(cmds, "") }

func rootHelpFor(cmds []*command, prefix string) string {
	var b bytes.Buffer
	if prefix == "" {
		b.WriteString("backup-cli: full, independent backups at incremental speed. Restore with cp.\n\n")
		b.WriteString("Usage: backup-cli [--state-dir DIR] [--unit-dir DIR] [--verbose] <command> [args]\n\n")
	} else {
		fmt.Fprintf(&b, "Usage: backup-cli %s<command> [args]\n\n", prefix)
	}
	b.WriteString("Commands:\n")
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, c := range cmds {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.summary)
	}
	if prefix == "" {
		_, _ = fmt.Fprintf(tw, "  help\tShow help for a command\n")
	}
	_ = tw.Flush()
	if prefix == "" {
		b.WriteString("\nGlobal flags (before the command):\n")
		b.WriteString("  --state-dir DIR   state directory (env BACKUP_CLI_STATE_DIR, default /var/lib/backup-cli)\n")
		b.WriteString("  --unit-dir DIR    systemd unit directory (env BACKUP_CLI_UNIT_DIR, default /etc/systemd/system)\n")
		b.WriteString("  --verbose, -v     verbose diagnostics on stderr\n")
		b.WriteString("\nExit codes: 0 ok, 1 failed, 2 usage, 3 partial, 4 refused.\n")
		b.WriteString("Use 'backup-cli help <command>' for details.\n")
	}
	return b.String()
}

func groupHelp(c *command, prefix string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: backup-cli %s%s <subcommand> [flags]\n\n%s\n\nSubcommands:\n", prefix, c.name, c.summary)
	subs := append([]*command(nil), c.subs...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].name < subs[j].name })
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	for _, s := range subs {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", s.name, s.summary)
	}
	_ = tw.Flush()
	return b.String()
}

func commandHelp(a *app, c *command, prefix string) string {
	fs := newFlagSet(c.name)
	c.setup(a, fs)
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: backup-cli %s\n\n%s\n", prefix+c.usage, c.summary)
	if c.long != "" {
		b.WriteString("\n" + c.long + "\n")
	}
	var fb bytes.Buffer
	fs.SetOutput(&fb)
	fs.PrintDefaults()
	if fb.Len() > 0 {
		b.WriteString("\nFlags:\n" + fb.String())
	}
	return b.String()
}

func newFlagSet(name string) *flagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// execute parses the command's flags (which may follow positional
// arguments), checks the argument count and runs it.
func (a *app) execute(c *command, prefix string, args []string) error {
	fs := newFlagSet(c.name)
	run := c.setup(a, fs)
	pos, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		_, werr := io.WriteString(a.out(), commandHelp(a, c, prefix))
		return werr
	}
	if err != nil {
		return exitcode.Usagef("%s: %v\nusage: backup-cli %s", prefix+c.name, err, prefix+c.usage)
	}
	if len(pos) != c.nargs {
		return exitcode.Usagef("%s: expected %d argument(s), got %d\nusage: backup-cli %s", prefix+c.name, c.nargs, len(pos), prefix+c.usage)
	}
	return run(pos)
}

// parseInterspersed parses flags that may appear before, between or after
// positional arguments. A literal "--" ends flag parsing.
func parseInterspersed(fs *flagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if len(args) > len(rest) && args[len(args)-len(rest)-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}
