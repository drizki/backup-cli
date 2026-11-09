package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/space"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/walk"
)

// prevBackup is the newest committed backup, used to seed unchanged files.
type prevBackup struct {
	dir     string
	marker  backupset.Marker
	entries map[string]manifest.Entry
}

// fileJob is one regular file to copy. A non-empty prevSHA means "copy from
// the previous backup and check against this checksum".
type fileJob struct {
	idx     int
	prevSHA string
}

// plan is everything decided before the first byte is copied.
type plan struct {
	entries    []manifest.Entry
	skipped    []walk.Skipped
	files      []fileJob
	prev       *prevBackup
	partialDir string
	resumed    bool
	replay     map[string]journal.Entry
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
	p.entries, p.skipped = res.Entries, res.Skipped
	describeSkipped(r, p)
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
	r.log.Infof("resuming the partial backup %s (%d file(s) journaled)", found.Path, len(p.replay))
	return r.checkFinalFree()
}

func (r *run) checkFinalFree() error {
	if _, err := os.Lstat(r.finalDir); err == nil {
		return exitcode.Refusedf("backup directory %s already exists", r.finalDir)
	}
	return nil
}

// checkPartialOwner proves that dir is this job's own partial backup. A
// directory it cannot prove ownership of is never touched.
func checkPartialOwner(dir, jobName string, p *plan) error {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return exitcode.Refusedf("%s is not a plain directory; refusing to reuse it", dir)
	}
	rep, err := journal.Read(filepath.Join(dir, journal.Name))
	if errors.Is(err, os.ErrNotExist) {
		m, merr := backupset.ReadMarker(dir)
		if merr != nil || m.Job != jobName {
			return exitcode.Refusedf("%s has no journal and no marker of job %q; refusing to reuse it (inspect it and move it away)", dir, jobName)
		}
		return nil
	}
	if err != nil {
		return exitcode.Refusedf("%s: unusable journal: %v", dir, err)
	}
	if rep.Job != jobName {
		return exitcode.Refusedf("%s belongs to job %q, not %q", dir, rep.Job, jobName)
	}
	p.replay = rep.Entries
	return nil
}

// loadPrev reads the newest committed backup's manifest. Any problem is a
// warning: the run then reads everything from the source.
func (r *run) loadPrev() *prevBackup {
	committed := backupset.OnlyCommitted(r.backups)
	if len(committed) == 0 {
		return nil
	}
	b := committed[len(committed)-1]
	raw, err := os.ReadFile(filepath.Join(b.Path, manifest.TSVName))
	if err != nil {
		r.warn("previous backup %s is unusable (%v); reading everything from the source", b.Name, err)
		return nil
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != b.Marker.ManifestSHA256 {
		r.warn("previous backup %s: manifest does not match its marker; reading everything from the source", b.Name)
		return nil
	}
	entries, err := manifest.ParseTSV(bytes.NewReader(raw))
	if err != nil {
		r.warn("previous backup %s: %v; reading everything from the source", b.Name, err)
		return nil
	}
	m := make(map[string]manifest.Entry, len(entries))
	for _, e := range entries {
		m[e.Path] = e
	}
	return &prevBackup{dir: b.Path, marker: *b.Marker, entries: m}
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

// lookup returns the previous backup's entry for path; safe on nil.
func (pb *prevBackup) lookup(path string) (manifest.Entry, bool) {
	if pb == nil {
		return manifest.Entry{}, false
	}
	e, ok := pb.entries[path]
	return e, ok
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
		if ok && unchanged(e, pe) {
			p.files = append(p.files, fileJob{idx: i, prevSHA: pe.SHA256})
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

func unchanged(cur, prev manifest.Entry) bool {
	return prev.Type == manifest.TypeFile && prev.Size == cur.Size && prev.MtimeNS == cur.MtimeNS && prev.SHA256 != ""
}
