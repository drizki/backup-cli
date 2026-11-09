package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/lock"
	"github.com/drizki/backup-cli/internal/state"
)

// checkLastRun reports the job's newest run and returns it (nil if none).
func checkLastRun(c *collector, env Env, j job.Job) *state.Status {
	runs, _ := env.Store.ListRuns(j.Name)
	if len(runs) == 0 {
		sev := OK
		if j.Scheduled {
			sev = Warn
		}
		c.add(sev, "last run", j.Name, "this job has never run")
		return nil
	}
	st := runs[0]
	switch {
	case st.State == state.StateFailed:
		c.add(Fail, "last run", j.Name, "%s failed (exit %d): %s; see 'backup-cli tail %s'", st.RunID, st.ExitCode, firstError(st), st.RunID)
	case st.State == state.StatePartial, st.State == state.StateRefused:
		c.add(Warn, "last run", j.Name, "%s ended %s (exit %d): %s; see 'backup-cli tail %s'", st.RunID, st.State, st.ExitCode, firstError(st), st.RunID)
	case st.State == state.StateRunning:
		checkRunning(c, env, j.Name, st)
	case !st.Finished.IsZero() && env.Now().Sub(st.Finished) > StaleRunAge:
		c.add(Warn, "last run", j.Name, "%s finished %s ago; the job looks idle", st.RunID, env.Now().Sub(st.Finished).Round(time.Hour))
	default:
		c.add(OK, "last run", j.Name, "%s %s", st.RunID, st.State)
	}
	return &st
}

func checkRunning(c *collector, env Env, name string, st state.Status) {
	if pidAlive(env, st.PID) {
		c.add(OK, "last run", name, "%s is running (pid %d)", st.RunID, st.PID)
		return
	}
	c.add(Warn, "last run", name, "%s is recorded as running but pid %d is gone; the run was interrupted, run the job again to resume", st.RunID, st.PID)
}

func firstError(st state.Status) string {
	switch {
	case len(st.Errors) > 0:
		return st.Errors[0]
	case st.RefuseReason != "":
		return st.RefuseReason
	}
	return "no error recorded"
}

func pidAlive(env Env, pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat(filepath.Join(env.ProcRoot, strconv.Itoa(pid)))
	return err == nil
}

// checkLock inspects the job's lock file. Lock files are kept after a run, so
// a file that is not held is normal; it is stale only when a crashed run left
// it behind or something that is not the recorded process holds it.
func checkLock(c *collector, env Env, j job.Job, last *state.Status) {
	path := env.Store.LockPath(j.Name)
	held, err := lock.Held(path)
	if err != nil {
		c.add(Warn, "lock", j.Name, "cannot inspect %s: %v", path, err)
		return
	}
	pid := lockPID(path)
	switch {
	case held && !pidAlive(env, pid):
		c.add(Warn, "lock", j.Name, "%s is held but the recorded pid %d is not running; a leftover descriptor may hold it", path, pid)
	case held:
		c.add(OK, "lock", j.Name, "held by running pid %d", pid)
	case pid > 0 && !pidAlive(env, pid) && last != nil && last.State == state.StateRunning:
		c.add(Warn, "lock", j.Name, "stale: %s names pid %d, which is gone, and the last run never finished. The lock itself is free; the next run reuses it", path, pid)
	case pid > 0:
		c.add(OK, "lock", j.Name, "lock file present, not held (normal; lock files are kept)")
	default:
		c.add(OK, "lock", j.Name, "no lock file yet")
	}
}

// lockPID reads the PID the last holder recorded, or 0.
func lockPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}
