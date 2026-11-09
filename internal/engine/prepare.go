package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/space"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/walk"
)

// fileJob is one regular file to copy. A true fromPrev means "copy from the
// previous backup and check against its recorded checksum".
type fileJob struct {
	idx      int
	fromPrev bool
}

// plan is everything decided before the first byte is copied.
type plan struct {
	entries    []manifest.Entry
	skipped    []walk.Skipped
	files      []fileJob
	prev       *prevBackup
	partialDir string
	resumed    bool
	replay     journal.Replay
	needBytes  uint64
	freeBytes  uint64
	removed    int // paths of the previous backup that no longer exist
	srcCount   int64
	srcBytes   int64
	prevCount  int64
	prevBytes  int64
}

func (r *run) prepare(ctx context.Context) (*plan, error) {
	if err := r.guards(); err != nil {
		return nil, err
	}
	p := &plan{}
	if err := r.locatePartial(p); err != nil {
		return nil, err
	}
	res, err := walk.Walk(ctx, r.j.Source)
	if err != nil {
		return nil, exitcode.Failedf("walk %s: %w", r.j.Source, err)
	}
	if err := rootCollision(res.Entries); err != nil {
		return nil, err
	}
	if err := r.checkShrink(p, res.Files); err != nil {
		return nil, err
	}
	p.entries, p.skipped = res.Entries, res.Skipped
	describeSkipped(r, p)
	for _, v := range res.Vanished {
		r.warn("%s vanished while the tree was being listed; it is not in this backup", v)
	}
	if !r.req.FromSource {
		p.prev = r.loadPrev()
	}
	if err := r.checkSpace(p, res.Bytes); err != nil {
		return nil, err
	}
	r.classify(p)
	r.update(func(s *state.Status) {
		s.FilesTotal, s.BytesTotal = res.Files, res.Bytes
		s.PartialDir, s.FinalDir = p.partialDir, r.finalDir
	})
	return p, nil
}

// guards runs the cheap safety checks that need no walk.
func (r *run) guards() error {
	fi, err := os.Stat(r.j.Source)
	if err != nil || !fi.IsDir() {
		return exitcode.Refusedf("source %s is not a readable directory", r.j.Source)
	}
	if err := probeReadable(r.j.Source); err != nil {
		return exitcode.Refusedf("source %s is not readable: %v", r.j.Source, err)
	}
	if err := r.j.CheckOverlap(); err != nil {
		return exitcode.Refusedf("%v", err)
	}
	parent := r.j.DstParent()
	if fi, err := os.Stat(parent); err != nil || !fi.IsDir() {
		return exitcode.Refusedf("destination directory %s does not exist", parent)
	}
	if err := syscall.Access(parent, 2); err != nil {
		return exitcode.Refusedf("destination directory %s is not writable: %v", parent, err)
	}
	return nil
}

func probeReadable(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// locatePartial decides whether to resume a leftover partial directory and
// fixes the final directory name.
func (r *run) locatePartial(p *plan) error {
	var found *backupset.Backup
	for i := range r.backups {
		if r.backups[i].Status == backupset.Partial {
			found = &r.backups[i]
		}
	}
	if found == nil {
		r.finalDir = r.j.DstFor(r.start)
		p.partialDir = r.finalDir + backupset.PartialSuffix
		return r.checkFinalFree()
	}
	if err := checkPartialOwner(found.Path, r.j.Name, p); err != nil {
		return err
	}
	p.resumed, p.partialDir = true, found.Path
	r.finalDir = strings.TrimSuffix(found.Path, backupset.PartialSuffix)
	r.log.Infof("resuming the partial backup %s (%d file(s) journaled)", found.Path, len(p.replay.Entries))
	return r.checkFinalFree()
}

func (r *run) checkFinalFree() error {
	if _, err := os.Lstat(r.finalDir); err == nil {
		return exitcode.Refusedf("backup directory %s already exists", r.finalDir)
	}
	return nil
}

// checkPartialOwner proves that dir is this job's own partial backup. A
// directory it cannot prove ownership of is never touched. A partial that
// holds nothing but an empty or headerless journal (a run killed right after
// creating it) has no data to lose and counts as the job's own.
func checkPartialOwner(dir, jobName string, p *plan) error {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return exitcode.Refusedf("%s is not a plain directory; refusing to reuse it", dir)
	}
	rep, err := journal.Read(filepath.Join(dir, journal.Name))
	if errors.Is(err, os.ErrNotExist) {
		return ownedWithoutJournal(dir, jobName)
	}
	if err != nil {
		return exitcode.Refusedf("%s: unusable journal: %v", dir, err)
	}
	if rep.Job == "" && holdsOnly(dir, journal.Name) {
		p.replay = rep
		return nil
	}
	if rep.Job != jobName {
		return exitcode.Refusedf("%s belongs to job %q, not %q", dir, rep.Job, jobName)
	}
	p.replay = rep
	return nil
}

func ownedWithoutJournal(dir, jobName string) error {
	if m, err := backupset.ReadMarker(dir); err == nil && m.Job == jobName {
		return nil
	}
	if holdsOnly(dir) {
		return nil
	}
	return exitcode.Refusedf("%s has no journal and no marker of job %q; refusing to reuse it (inspect it and move it away)", dir, jobName)
}

// holdsOnly reports whether dir contains nothing but the named entries.
func holdsOnly(dir string, names ...string) bool {
	items, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, it := range items {
		if !slices.Contains(names, it.Name()) {
			return false
		}
	}
	return true
}

func (r *run) checkSpace(p *plan, srcBytes int64) error {
	var last int64
	if p.prev != nil {
		last = p.prev.marker.Bytes
	}
	stat := r.e.Statfs
	if stat == nil {
		stat = space.Free
	}
	p.needBytes = space.Required(last, srcBytes)
	have, err := stat(r.j.DstParent())
	if err != nil {
		return exitcode.Failedf("free space of %s: %v", r.j.DstParent(), err)
	}
	p.freeBytes = have
	if have < p.needBytes {
		return exitcode.With(exitcode.Refused, &space.ShortError{Need: p.needBytes, Have: have})
	}
	return nil
}

// classify splits the files into those unchanged since the previous backup
// and those that must be read from the source.
func (r *run) classify(p *plan) {
	seen := 0
	for i, e := range p.entries {
		pe, ok := p.prev.lookup(e.Path)
		if ok {
			seen++
		}
		if e.Type != manifest.TypeFile {
			continue
		}
		if ok && unchanged(e, pe) && !racy(e.MtimeNS, p.prev.marker.Started) {
			p.files = append(p.files, fileJob{idx: i, fromPrev: true})
			p.prevCount++
			p.prevBytes += e.Size
			continue
		}
		p.files = append(p.files, fileJob{idx: i})
		p.srcCount++
		p.srcBytes += e.Size
	}
	if p.prev != nil {
		p.removed = len(p.prev.entries) - seen
	}
}

// racyMargin widens the window of "possibly rewritten within the same
// timestamp tick" around a run's start; coarse filesystems tick every 1-2 s.
const racyMargin = 2 * time.Second

// racy reports whether a file with this mtime might have been rewritten after
// a run that started at started copied it, without its size or mtime
// changing. A zero start (an older marker or journal) never matches.
func racy(mtimeNS int64, started time.Time) bool {
	return !started.IsZero() && mtimeNS >= started.Add(-racyMargin).UnixNano()
}

func unchanged(cur manifest.Entry, prev prevEntry) bool {
	return prev.file && prev.size == cur.Size && prev.mtimeNS == cur.MtimeNS
}
