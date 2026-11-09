// Package cli is the command-line front end: a small table of commands, a
// hand-written dispatcher on top of the standard flag package, output
// formatting and exit-code mapping.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

// ErrUnavailable is returned by commands whose engine is not wired into the build.
var ErrUnavailable = errors.New("not available in this build yet")

// EngineRequest is what the CLI hands to the run engine.
type EngineRequest struct {
	Store        state.Store
	Job          job.Job
	DryRun       bool
	FromSource   bool
	Force        bool
	Jobs         int // 0 = use the job's setting
	VerifySample int // -1 = use the job's setting
	Stdout       io.Writer
	Stderr       io.Writer
}

// EngineFunc is the run engine entry point. The engine package registers it.
type EngineFunc func(ctx context.Context, req EngineRequest) error

// Deps are the injectable collaborators of the CLI.
type Deps struct {
	Stdout, Stderr io.Writer
	Stdin          io.Reader
	Getenv         func(string) string
	// Manager builds the systemd Manager for a unit directory.
	Manager func(unitDir string) systemd.Manager
	// Executable returns the absolute path of this binary.
	Executable func() (string, error)
	// Analyze optionally validates oncalendar expressions (systemd-analyze).
	Analyze func(expr string) error
	// Engine runs a job in the foreground; nil means the engine is absent.
	Engine EngineFunc
}

// registeredEngine is set by the engine package when it is linked in.
var registeredEngine EngineFunc

// RegisterEngine installs the engine used by DefaultDeps.
func RegisterEngine(f EngineFunc) { registeredEngine = f }

// DefaultDeps wires the production collaborators.
func DefaultDeps() Deps {
	return Deps{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Stdin:      os.Stdin,
		Getenv:     os.Getenv,
		Manager:    func(dir string) systemd.Manager { return systemd.NewReal(dir) },
		Executable: executablePath,
		Analyze:    systemdAnalyze,
		Engine:     registeredEngine,
	}
}

func executablePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// systemdAnalyze validates a calendar expression when systemd-analyze exists.
func systemdAnalyze(expr string) error {
	bin, err := exec.LookPath("systemd-analyze")
	if err != nil {
		return nil // cannot validate; the strict charset check still applies
	}
	out, err := exec.Command(bin, "calendar", expr).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-analyze: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

type app struct {
	ctx      context.Context
	d        Deps
	stateDir string
	unitDir  string
	verbose  bool
}

func (a *app) out() io.Writer { return a.d.Stdout }
func (a *app) err() io.Writer { return a.d.Stderr }

func (a *app) manager() systemd.Manager { return a.d.Manager(a.unitDir) }

func (a *app) store() (state.Store, error) {
	dir, err := filepath.Abs(a.stateDir)
	if err != nil {
		return state.Store{}, exitcode.With(exitcode.Usage, err)
	}
	return state.New(dir), nil
}

// debugf prints a diagnostic line to stderr in verbose mode.
func (a *app) debugf(format string, v ...any) {
	if a.verbose {
		_, _ = fmt.Fprintf(a.err(), "debug: "+format+"\n", v...)
	}
}

// Run executes the CLI with args (without the program name) and returns the
// process exit code. Errors are printed to d.Stderr.
func Run(ctx context.Context, args []string, d Deps) int {
	if d.Getenv == nil {
		d.Getenv = func(string) string { return "" }
	}
	if d.Stdout == nil {
		d.Stdout = io.Discard
	}
	if d.Stderr == nil {
		d.Stderr = io.Discard
	}
	if d.Stdin == nil {
		d.Stdin = strings.NewReader("")
	}
	a := &app{ctx: ctx, d: d, stateDir: state.DefaultDir, unitDir: systemd.DefaultUnitDir}
	if v := d.Getenv("BACKUP_CLI_STATE_DIR"); v != "" {
		a.stateDir = v
	}
	if v := d.Getenv("BACKUP_CLI_UNIT_DIR"); v != "" {
		a.unitDir = v
	}
	err := a.main(args)
	if err == nil {
		return exitcode.OK
	}
	code := exitcode.Code(err)
	_, _ = fmt.Fprintf(d.Stderr, "error: %v\n", err)
	if code == exitcode.Usage {
		_, _ = fmt.Fprintln(d.Stderr, "Run 'backup-cli help' for usage.")
	}
	return code
}

// Execute is the program entry point.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, os.Args[1:], DefaultDeps())
}

// main parses the global flags that precede the subcommand, then dispatches.
func (a *app) main(args []string) error {
	cmds := commands()
	for len(args) > 0 {
		rest, ok, err := a.parseGlobal(args)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		args = rest
	}
	if len(args) == 0 {
		return exitcode.Usagef("missing command\n\n%s", rootHelp(cmds))
	}
	return a.dispatch(cmds, "", args)
}

// parseGlobal consumes one global flag from the front of args. ok is false
// when args[0] is not a global flag.
func (a *app) parseGlobal(args []string) (rest []string, ok bool, err error) {
	name, val, hasVal := strings.Cut(args[0], "=")
	switch name {
	case "--verbose", "-v":
		a.verbose = true
		return args[1:], true, nil
	case "--state-dir", "--unit-dir":
		if !hasVal {
			if len(args) < 2 {
				return nil, false, exitcode.Usagef("flag %s needs a value", name)
			}
			val, args = args[1], args[1:]
		}
		if name == "--state-dir" {
			a.stateDir = val
		} else {
			a.unitDir = val
		}
		return args[1:], true, nil
	}
	return args, false, nil
}

func versionCmd() *command {
	return &command{
		name: "version", summary: "Print the version", usage: "version", nargs: 0,
		setup: func(a *app, _ *flagSet) runFunc {
			return func([]string) error {
				_, err := fmt.Fprintf(a.out(), "backup-cli %s\n", versionString())
				return err
			}
		},
	}
}
