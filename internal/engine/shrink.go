package engine

import (
	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
)

// checkShrink refuses a run whose source holds fewer than half the files of
// the newest committed backup or of the partial being resumed. An unmounted
// share is an empty, readable directory; without this check it would be
// committed as an empty backup and retention would prune the good ones. It
// runs after the walk and before anything is swept, copied or pruned.
func (r *run) checkShrink(p *plan, walked int64) error {
	if r.req.Force {
		return nil
	}
	base, what := int64(len(p.replay.Entries)), "the partial backup being resumed"
	if committed := backupset.OnlyCommitted(r.backups); len(committed) > 0 {
		last := committed[len(committed)-1]
		if last.Marker.FileCount > base {
			base, what = last.Marker.FileCount, "the newest backup "+last.Name
		}
	}
	if walked*2 >= base {
		return nil
	}
	return exitcode.Refusedf("source %s holds %d file(s), fewer than half of the %d in %s; "+
		"the source may not be mounted. Check it, or run again with --force if the drop is expected",
		r.j.Source, walked, base, what)
}
