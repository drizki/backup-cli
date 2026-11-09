// Package doctor inspects the environment, every job and its backups and
// reports problems. It only reads: nothing is created, changed or deleted.
// Every outside source (/sys, /proc, mountinfo, statfs, systemd, the clock,
// access checks) is injectable.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/drizki/backup-cli/internal/mount"
	"github.com/drizki/backup-cli/internal/space"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

// Severity grades a check.
type Severity string

// Severities. Warnings do not fail doctor; failures do.
const (
	OK   Severity = "ok"
	Warn Severity = "warn"
	Fail Severity = "fail"
)

// Access modes for Env.Access.
const (
	CanRead  uint32 = 4
	CanWrite uint32 = 2
)

// StaleRunAge is how old a job's last finished run may be before doctor warns.
const StaleRunAge = 40 * 24 * time.Hour

// Check is one finding.
type Check struct {
	Name     string   `json:"name"`
	Job      string   `json:"job,omitempty"`
	Severity Severity `json:"severity"`
	Detail   string   `json:"detail"`
}

// Report is the full result.
type Report struct {
	Version  string  `json:"version"`
	OK       bool    `json:"ok"`
	Failures int     `json:"failures"`
	Warnings int     `json:"warnings"`
	Checks   []Check `json:"checks"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool { return r.Failures > 0 }

// Env holds the collaborators; zero fields take production defaults.
type Env struct {
	Store     state.Store
	Manager   systemd.Manager // nil means systemd is unavailable
	SysRoot   string          // default /sys
	ProcRoot  string          // default /proc
	MountInfo string          // default /proc/self/mountinfo
	Statfs    space.StatFunc  // default statfs(2)
	// Access reports whether the calling account may read or write path.
	Access  func(path string, mode uint32) error
	Now     func() time.Time
	Version string
}

func (e Env) withDefaults() Env {
	if e.SysRoot == "" {
		e.SysRoot = "/sys"
	}
	if e.ProcRoot == "" {
		e.ProcRoot = "/proc"
	}
	if e.MountInfo == "" {
		e.MountInfo = mount.DefaultInfo
	}
	if e.Statfs == nil {
		e.Statfs = space.Free
	}
	if e.Access == nil {
		e.Access = syscall.Access
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	return e
}

// collector gathers checks.
type collector struct{ checks []Check }

func (c *collector) add(sev Severity, name, job, format string, a ...any) {
	c.checks = append(c.checks, Check{Name: name, Job: job, Severity: sev, Detail: fmt.Sprintf(format, a...)})
}

// Run performs every check.
func Run(ctx context.Context, env Env) Report {
	env = env.withDefaults()
	c := &collector{}
	checkState(c, env)
	checkSystemd(c, env)
	checkZFS(c, env)
	jobs, errs := env.Store.ListJobs()
	for _, err := range errs {
		c.add(Fail, "job file", "", "%v", err)
	}
	if len(jobs) == 0 && len(errs) == 0 {
		c.add(OK, "jobs", "", "no jobs defined")
	}
	for _, j := range jobs {
		if ctx.Err() != nil {
			break
		}
		checkJob(c, env, j)
	}
	c.add(OK, "version", "", "%s", env.Version)
	return finish(env.Version, c.checks)
}

func finish(version string, checks []Check) Report {
	r := Report{Version: version, Checks: checks}
	for _, ch := range checks {
		switch ch.Severity {
		case Fail:
			r.Failures++
		case Warn:
			r.Warnings++
		}
	}
	r.OK = r.Failures == 0
	return r
}

// WriteJSON renders the report as indented JSON.
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText renders the report for humans.
func (r Report) WriteText(w io.Writer) error {
	tags := map[Severity]string{OK: "ok  ", Warn: "WARN", Fail: "FAIL"}
	for _, ch := range r.Checks {
		who := ch.Name
		if ch.Job != "" {
			who += " [" + ch.Job + "]"
		}
		if _, err := fmt.Fprintf(w, "[%s] %s: %s\n", tags[ch.Severity], who, ch.Detail); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\n%d check(s), %d warning(s), %d failure(s)\n", len(r.Checks), r.Warnings, r.Failures)
	return err
}
