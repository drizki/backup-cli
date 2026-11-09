package doctor

import (
	"path/filepath"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/memlimit"
	"github.com/drizki/backup-cli/internal/space"
)

// checkMemory warns when a run of the last backup's size is expected to need
// more memory than the cgroup limit allows, so the kernel would kill it.
func checkMemory(c *collector, env Env, j job.Job, committed []backupset.Backup) {
	limit, ok := memlimit.Read(filepath.Join(env.SysRoot, "fs/cgroup/memory.max"))
	if !ok || len(committed) == 0 {
		return
	}
	files := committed[len(committed)-1].Marker.FileCount
	need := uint64(max(files, 0)) * memlimit.PeakPerFile
	if need > uint64(limit) {
		c.add(Warn, "memory", j.Name, "the last backup held %d file(s), which needs about %s of memory (about %d bytes per file), "+
			"but the memory limit is %s; the run may be killed. Split the source into several jobs or raise the limit",
			files, space.Format(need), memlimit.PeakPerFile, space.Format(uint64(limit)))
		return
	}
	c.add(OK, "memory", j.Name, "%d file(s) need about %s of memory; the limit is %s", files, space.Format(need), space.Format(uint64(limit)))
}
