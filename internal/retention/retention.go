// Package retention chooses which committed backups to prune and removes
// them through safedelete. It runs only after a fully successful run.
package retention

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/runlog"
	"github.com/drizki/backup-cli/internal/safedelete"
)

// Plan returns the committed backups to remove, oldest first: everything
// beyond the newest keep, ordered by marker creation time (then name).
// Anything that is not a committed backup (partial, marker-less, bad or
// foreign directories) is ignored. keep below 1 is treated as 1, so the last
// good backup is never selected.
func Plan(backups []backupset.Backup, keep int) []backupset.Backup {
	keep = max(keep, 1)
	var committed []backupset.Backup
	for _, b := range backups {
		if b.IsCommitted() && b.Marker != nil {
			committed = append(committed, b)
		}
	}
	sort.SliceStable(committed, func(a, b int) bool {
		ta, tb := committed[a].Marker.Created, committed[b].Marker.Created
		if !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return committed[a].Name < committed[b].Name
	})
	if len(committed) <= keep {
		return nil
	}
	return committed[:len(committed)-keep]
}

// Prune removes the backups Plan selects, never the backup at current (the
// one just committed). Each removal goes through the guard; a refusal or
// failure is logged and the remaining backups are still considered. The
// returned error joins every failure.
func Prune(ctx context.Context, g safedelete.Guard, j job.Job, committed []backupset.Backup, current string, log *runlog.Log) error {
	var errs []error
	for _, b := range Plan(committed, j.Keep) {
		if ctx.Err() != nil {
			return errors.Join(append(errs, ctx.Err())...)
		}
		if b.Path == current {
			log.Warnf("retention: not removing %s, it is the backup just made", b.Name)
			continue
		}
		if err := g.Delete(j, b.Path); err != nil {
			log.Warnf("retention: %s kept: %v", b.Name, err)
			errs = append(errs, fmt.Errorf("%s: %w", b.Name, err))
			continue
		}
		log.Infof("retention: removed %s (keep %d)", b.Name, j.Keep)
	}
	return errors.Join(errs...)
}

// Func returns the callback the engine runs after a successful run, bound to
// the state directory the guard protects.
func Func(stateDir string) func(context.Context, job.Job, []backupset.Backup, string, *runlog.Log) error {
	g := safedelete.Guard{StateDir: stateDir}
	return func(ctx context.Context, j job.Job, committed []backupset.Backup, current string, log *runlog.Log) error {
		return Prune(ctx, g, j, committed, current, log)
	}
}
