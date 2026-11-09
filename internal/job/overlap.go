package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/pathguard"
)

// CheckOverlap refuses configurations where the source and the backups of
// this job could contain each other, in either direction and through
// symlinks:
//
//   - the destination parent equals or lies inside the source (a backup would
//     be written into the tree being copied), or
//   - the source lies inside one of the job's own backup directories (a
//     directory named after the template, or its .partial form).
//
// Other directories sharing the destination parent with the source are fine.
// Paths need not exist yet.
func (j Job) CheckOverlap() error {
	src, err := pathguard.Resolve(j.Source)
	if err != nil {
		return err
	}
	parent, err := pathguard.Resolve(j.DstParent())
	if err != nil {
		return err
	}
	if pathguard.Contains(src, parent) {
		return fmt.Errorf("destination %s is the same as or inside the source %s", j.DstParent(), j.Source)
	}
	if pathguard.Contains(parent, src) {
		rel, err := filepath.Rel(parent, src)
		if err != nil {
			return err
		}
		first, _, _ := strings.Cut(rel, string(os.PathSeparator))
		if j.MatchesName(strings.TrimSuffix(first, ".partial")) {
			return fmt.Errorf("source %s is inside the backup directory %s", j.Source, filepath.Join(parent, first))
		}
	}
	return nil
}
