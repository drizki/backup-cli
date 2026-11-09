package engine

import (
	"io/fs"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/safedelete"
)

// sweep removes from this run's own partial directory everything the plan
// does not contain: files deleted from the source since an interrupted run,
// half-written or temporary objects, and objects of the wrong type. Without
// it such leftovers would be committed with no manifest entry, or block the
// planned object from being created. The removal itself is safedelete's.
func (r *run) sweep(p *plan) error {
	want := make(map[string]fs.FileMode, len(p.entries))
	for _, e := range p.entries {
		if e.Path != manifest.RootPath {
			want[e.Path] = typeBits(e.Type)
		}
	}
	removed, err := safedelete.SweepPartial(p.partialDir, want, journal.Name)
	for _, rel := range removed {
		r.log.Infof("removed %s from the partial backup: it is not part of this run", rel)
	}
	if err != nil {
		return exitcode.Failedf("clean the partial backup: %v", err)
	}
	return nil
}

func typeBits(typ string) fs.FileMode {
	switch typ {
	case manifest.TypeDir:
		return fs.ModeDir
	case manifest.TypeSymlink:
		return fs.ModeSymlink
	}
	return 0
}
