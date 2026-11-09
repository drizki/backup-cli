package doctor

import (
	"fmt"
	"os"
	"strings"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/mount"
	"github.com/drizki/backup-cli/internal/pathguard"
	"github.com/drizki/backup-cli/internal/space"
)

// checkJob runs every per-job check.
func checkJob(c *collector, env Env, j job.Job) {
	checkSource(c, env, j)
	if err := j.CheckOverlap(); err != nil {
		c.add(Fail, "overlap", j.Name, "%v", err)
	} else {
		c.add(OK, "overlap", j.Name, "source and destination do not overlap")
	}
	if !checkDestination(c, env, j) {
		return
	}
	backups, err := backupset.Scan(j)
	if err != nil {
		c.add(Fail, "backups", j.Name, "cannot scan %s: %v", j.DstParent(), err)
		return
	}
	committed := backupset.OnlyCommitted(backups)
	c.add(OK, "backups", j.Name, "%d committed backup(s), keep %d", len(committed), j.Keep)
	checkSpace(c, env, j, committed)
	checkLeftovers(c, j, backups)
	last := checkLastRun(c, env, j)
	checkLock(c, env, j, last)
	checkTimer(c, env, j.Name, j.Scheduled)
}

func checkSource(c *collector, env Env, j job.Job) {
	fi, err := os.Stat(j.Source)
	switch {
	case err != nil:
		c.add(Fail, "source readable", j.Name, "%v", err)
		return
	case !fi.IsDir():
		c.add(Fail, "source readable", j.Name, "%s is not a directory", j.Source)
		return
	case env.Access(j.Source, CanRead) != nil:
		c.add(Fail, "source readable", j.Name, "%s is not readable by this account", j.Source)
		return
	}
	c.add(OK, "source readable", j.Name, "%s", j.Source)
	if env.Access(j.Source, CanWrite) == nil {
		c.add(Warn, "source writable", j.Name, "the account that runs backups can modify %s; prefer a read-only mount or a read-only account", j.Source)
		return
	}
	c.add(OK, "source writable", j.Name, "the source is read-only for this account")
}

// checkDestination reports whether the destination parent is usable.
func checkDestination(c *collector, env Env, j job.Job) bool {
	parent := j.DstParent()
	fi, err := os.Stat(parent)
	switch {
	case err != nil:
		c.add(Fail, "destination writable", j.Name, "%v", err)
		return false
	case !fi.IsDir():
		c.add(Fail, "destination writable", j.Name, "%s is not a directory", parent)
		return false
	case env.Access(parent, CanWrite) != nil:
		c.add(Fail, "destination writable", j.Name, "%s is not writable by this account", parent)
		return false
	}
	c.add(OK, "destination writable", j.Name, "%s", parent)
	checkMount(c, env, j, parent)
	return true
}

// checkMount warns when the destination sits on the root filesystem, the
// usual sign that a backup disk is not mounted.
func checkMount(c *collector, env Env, j job.Job, parent string) {
	real, err := pathguard.Resolve(parent)
	if err != nil {
		real = parent
	}
	m, found, err := mount.For(env.MountInfo, real)
	switch {
	case err != nil:
		c.add(Warn, "destination mount", j.Name, "cannot read the mount table: %v", err)
	case !found:
		c.add(Warn, "destination mount", j.Name, "%s is not covered by any mount in the mount table", parent)
	case m.Point == "/":
		c.add(Warn, "destination mount", j.Name, "%s is on the root filesystem (%s); if a backup disk should be mounted there, check that it is", parent, m.FSType)
	default:
		c.add(OK, "destination mount", j.Name, "%s is on %s (%s)", parent, m.Point, m.FSType)
	}
}

// checkSpace verifies that keep+1 backups of the last backup's size fit.
func checkSpace(c *collector, env Env, j job.Job, committed []backupset.Backup) {
	if len(committed) == 0 {
		c.add(OK, "free space", j.Name, "no backup yet, so the size is unknown")
		return
	}
	last := committed[len(committed)-1].Marker.Bytes
	free, err := env.Statfs(j.DstParent())
	if err != nil {
		c.add(Warn, "free space", j.Name, "cannot read free space: %v", err)
		return
	}
	more := max(1, j.Keep+1-len(committed))
	need := uint64(more)*(uint64(max(last, 0))+uint64(max(last, 0))/20) + space.Headroom
	if free < need {
		c.add(Fail, "free space", j.Name, "%s free, but room for %d more backup(s) of %s needs %s (keep %d + 1)",
			space.Format(free), more, space.Format(uint64(max(last, 0))), space.Format(need), j.Keep)
		return
	}
	c.add(OK, "free space", j.Name, "%s free; keep %d + 1 backups of %s fit", space.Format(free), j.Keep, space.Format(uint64(max(last, 0))))
}

// checkLeftovers reports .partial directories and directories that match the
// job's template but are not committed backups. Doctor never deletes them.
func checkLeftovers(c *collector, j job.Job, backups []backupset.Backup) {
	var partial, unknown []string
	for _, b := range backups {
		switch b.Status {
		case backupset.Partial:
			partial = append(partial, b.Name)
		case backupset.NoMarker, backupset.BadMarker, backupset.Foreign:
			unknown = append(unknown, fmt.Sprintf("%s (%s)", b.Name, b.Status))
		}
	}
	if len(partial) == 0 {
		c.add(OK, "leftover partial", j.Name, "no leftover .partial directories")
	} else {
		c.add(Warn, "leftover partial", j.Name, "%s; run the job again to resume, or remove with 'backup-cli rm'", strings.Join(partial, ", "))
	}
	if len(unknown) > 0 {
		c.add(Warn, "unrecognized directories", j.Name, "%s match the template but are not committed backups; they are never pruned or touched", strings.Join(unknown, ", "))
	}
}
