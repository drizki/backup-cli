package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/retention"
	"github.com/drizki/backup-cli/internal/space"
)

// report prints the plan of a dry run. Nothing is written anywhere.
func (r *run) report(p *plan) {
	w := r.req.Stdout
	if w == nil {
		return
	}
	prev := "none (everything is read from the source)"
	if p.prev != nil {
		prev = p.prev.dir
	}
	_, _ = fmt.Fprintf(w, "dry run for job %s: nothing was written\n", r.j.Name)
	_, _ = fmt.Fprintf(w, "  source:            %s\n", r.j.Source)
	_, _ = fmt.Fprintf(w, "  new backup:        %s\n", r.finalDir)
	_, _ = fmt.Fprintf(w, "  previous backup:   %s\n", prev)
	if p.resumed {
		_, _ = fmt.Fprintf(w, "  resumes partial:   %s (%d journaled file(s) would be re-verified)\n", p.partialDir, len(p.replay))
	}
	_, _ = fmt.Fprintf(w, "  copy from source:  %d file(s), %s\n", p.srcCount, space.Format(uint64(p.srcBytes)))
	_, _ = fmt.Fprintf(w, "  copy from previous backup: %d file(s), %s\n", p.prevCount, space.Format(uint64(p.prevBytes)))
	_, _ = fmt.Fprintf(w, "  not carried over:  %d path(s) of the previous backup no longer exist\n", p.removed)
	_, _ = fmt.Fprintf(w, "  skipped specials:  %d\n", len(p.skipped))
	_, _ = fmt.Fprintf(w, "  free space:        %s available, %s needed\n", space.Format(p.freeBytes), space.Format(p.needBytes))
	names := pruneNames(backupset.OnlyCommitted(r.backups), r.j.Keep)
	if len(names) == 0 {
		_, _ = fmt.Fprintf(w, "  prune plan:        nothing to remove (keep %d)\n", r.j.Keep)
		return
	}
	_, _ = fmt.Fprintf(w, "  prune plan:        would remove %d old backup(s) after a successful run (keep %d): %s\n",
		len(names), r.j.Keep, strings.Join(names, ", "))
}

// pruneNames lists the backups retention would remove once the new backup
// exists, using retention's own planner with the new backup as a stand-in.
func pruneNames(committed []backupset.Backup, keep int) []string {
	newest := backupset.Backup{Name: "(new backup)", Status: backupset.Committed,
		Marker: &backupset.Marker{Created: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)}}
	var names []string
	for _, b := range retention.Plan(append(slices.Clone(committed), newest), keep) {
		names = append(names, b.Name)
	}
	return names
}
