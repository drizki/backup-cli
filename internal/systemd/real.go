package systemd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/state"
)

// Runner executes a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner is the production Runner.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Real drives systemctl and writes units into UnitDir.
type Real struct {
	UnitDir string
	Run     Runner
	// RuntimeDir is checked by Available; defaults to /run/systemd/system.
	RuntimeDir string
	// LookPath finds binaries; defaults to exec.LookPath.
	LookPath func(string) (string, error)
}

// NewReal returns a Manager for the given unit dir ("" selects the default).
func NewReal(unitDir string) *Real {
	if unitDir == "" {
		unitDir = DefaultUnitDir
	}
	return &Real{UnitDir: unitDir, Run: ExecRunner, RuntimeDir: "/run/systemd/system", LookPath: exec.LookPath}
}

// Available reports whether systemctl exists and systemd is the running init.
func (r *Real) Available() bool {
	if _, err := r.LookPath("systemctl"); err != nil {
		return false
	}
	fi, err := os.Stat(r.RuntimeDir)
	return err == nil && fi.IsDir()
}

func (r *Real) servicePath(name string) string { return filepath.Join(r.UnitDir, name+".service") }
func (r *Real) timerPath(name string) string   { return filepath.Join(r.UnitDir, name+".timer") }

func (r *Real) ctl(args ...string) error {
	out, err := r.Run(context.Background(), "systemctl", args...)
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Install writes the service and timer files and reloads systemd.
func (r *Real) Install(u Unit) error {
	if err := os.MkdirAll(r.UnitDir, 0o755); err != nil {
		return err
	}
	if err := state.WriteFileAtomic(r.servicePath(u.Name()), []byte(u.ServiceText()), 0o644); err != nil {
		return err
	}
	if err := state.WriteFileAtomic(r.timerPath(u.Name()), []byte(u.TimerText()), 0o644); err != nil {
		return err
	}
	return r.ctl("daemon-reload")
}

// Enable enables and starts the timer. For a non-default unit dir the timer
// file is linked by absolute path.
func (r *Real) Enable(name string) error {
	target := name + ".timer"
	if filepath.Clean(r.UnitDir) != DefaultUnitDir {
		target = r.timerPath(name)
	}
	return r.ctl("enable", "--now", target)
}

// Disable stops and disables the timer.
func (r *Real) Disable(name string) error {
	return r.ctl("disable", "--now", name+".timer")
}

// Remove deletes the unit files (missing files are fine) and reloads.
func (r *Real) Remove(name string) error {
	for _, p := range []string{r.timerPath(name), r.servicePath(name)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return r.ctl("daemon-reload")
}

// IsActive reports whether the timer is active. A non-zero exit from
// `systemctl is-active` means inactive, not an error.
func (r *Real) IsActive(name string) (bool, error) {
	out, err := r.Run(context.Background(), "systemctl", "is-active", name+".timer")
	if err == nil {
		return strings.TrimSpace(string(out)) == "active", nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}

// Start launches the service without waiting for it to finish.
func (r *Real) Start(name string) error {
	return r.ctl("start", "--no-block", name+".service")
}
