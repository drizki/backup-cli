// Package systemd renders and manages the per-job service and timer units
// behind a small Manager interface, so tests never touch a real systemd.
package systemd

import (
	"fmt"
	"strings"
)

// DefaultUnitDir is where units are written unless overridden.
const DefaultUnitDir = "/etc/systemd/system"

// UnitPrefix prefixes every unit name this tool owns.
const UnitPrefix = "backup-cli-"

// Unit describes the service/timer pair of one job.
type Unit struct {
	Job        string // job name (valid job-name charset)
	Binary     string // absolute path of the backup-cli binary
	StateDir   string // absolute state directory passed to the service
	OnCalendar string // systemd OnCalendar expression
}

// UnitName is the base unit name (without .service/.timer) for a job.
func UnitName(job string) string { return UnitPrefix + job }

// Name returns the base unit name.
func (u Unit) Name() string { return UnitName(u.Job) }

// ServiceText renders the .service unit.
func (u Unit) ServiceText() string {
	var b strings.Builder
	b.WriteString("# Managed by backup-cli. Remove with: backup-cli job rm " + u.Job + "\n")
	b.WriteString("[Unit]\n")
	b.WriteString("Description=backup-cli job " + u.Job + "\n")
	b.WriteString("\n[Service]\n")
	b.WriteString("Type=oneshot\n")
	fmt.Fprintf(&b, "ExecStart=%s --state-dir %s run %s\n", quoteArg(u.Binary), quoteArg(u.StateDir), quoteArg(u.Job))
	b.WriteString("Nice=19\n")
	b.WriteString("IOSchedulingClass=idle\n")
	b.WriteString("CPUSchedulingPolicy=idle\n")
	return b.String()
}

// TimerText renders the .timer unit.
func (u Unit) TimerText() string {
	var b strings.Builder
	b.WriteString("# Managed by backup-cli. Remove with: backup-cli job rm " + u.Job + "\n")
	b.WriteString("[Unit]\n")
	b.WriteString("Description=backup-cli schedule for job " + u.Job + "\n")
	b.WriteString("\n[Timer]\n")
	b.WriteString("OnCalendar=" + u.OnCalendar + "\n")
	b.WriteString("Persistent=true\n")
	b.WriteString("RandomizedDelaySec=0\n")
	b.WriteString("Unit=" + u.Name() + ".service\n")
	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=timers.target\n")
	return b.String()
}

// quoteArg quotes a path for a systemd ExecStart line. `%` and `$` are
// doubled so systemd does not expand specifiers or environment variables.
func quoteArg(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, "$", "$$")
	if s == "" || strings.ContainsAny(s, " \t\"'\\;") {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		return `"` + s + `"`
	}
	return s
}

// Manager is the systemd integration surface. Names are base unit names as
// returned by UnitName.
type Manager interface {
	Available() bool
	Install(unit Unit) error
	Enable(name string) error  // enable and start the timer
	Disable(name string) error // stop and disable the timer
	Remove(name string) error  // delete the unit files and reload
	IsActive(name string) (bool, error)
	Start(name string) error // start the service detached (--no-block)
}
