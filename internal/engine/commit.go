package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/copyx"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/version"
)

// commit turns the finished partial directory into a backup: manifests, then
// the marker, then directory metadata, fsync, an atomic rename and an fsync
// of the parent. Only a renamed directory carrying the marker is committed.
func (r *run) commit(p *plan) error {
	if err := r.sweep(p); err != nil { // nothing outside the plan may be committed
		return err
	}
	sum, err := writeManifests(p.partialDir, p.entries)
	if err != nil {
		return exitcode.Failedf("write manifests: %v", err)
	}
	var files, bytes int64
	for _, e := range p.entries {
		if e.Type == manifest.TypeFile {
			files++
			bytes += e.Size
		}
	}
	err = backupset.WriteMarker(p.partialDir, backupset.Marker{
		Job: r.j.Name, RunID: r.id, ToolVersion: version.Version, Created: r.e.now().UTC(), Started: r.backupStarted(p),
		Source: r.j.Source, FileCount: files, Bytes: bytes, ManifestSHA256: sum,
	})
	if err != nil {
		return exitcode.Failedf("write marker: %v", err)
	}
	// The journal is this run's own bookkeeping and is not part of the backup.
	if err := journal.Discard(filepath.Join(p.partialDir, journal.Name)); err != nil {
		r.log.Warnf("journal could not be removed: %v", err)
	}
	if err := applyDirs(p); err != nil {
		return exitcode.Failedf("finish directories: %v", err)
	}
	if err := os.Rename(p.partialDir, r.finalDir); err != nil {
		return exitcode.Failedf("commit rename: %v", err)
	}
	if err := state.SyncDir(r.j.DstParent()); err != nil {
		return exitcode.Failedf("sync %s: %v", r.j.DstParent(), err)
	}
	r.log.Infof("committed %s: %d file(s), %d byte(s)", r.finalDir, files, bytes)
	return nil
}

// backupStarted is when the backup began: the first run start of a resumed
// partial, else this run's start. The next run treats files modified since
// then as changed.
func (r *run) backupStarted(p *plan) time.Time {
	s := r.start.UTC()
	if first := p.replay.Started; !first.IsZero() && first.Before(s) {
		s = first.UTC()
	}
	return s
}

// writeManifests writes both manifests with fsync and returns the SHA-256 of
// MANIFEST.tsv.
func writeManifests(dir string, entries []manifest.Entry) (string, error) {
	if err := writeSynced(filepath.Join(dir, manifest.SHA256Name), func(w io.Writer) error {
		return manifest.WriteSHA256(w, entries)
	}); err != nil {
		return "", err
	}
	h := sha256.New()
	err := writeSynced(filepath.Join(dir, manifest.TSVName), func(w io.Writer) error {
		return manifest.WriteTSV(io.MultiWriter(w, h), entries)
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

func writeSynced(path string, fill func(io.Writer) error) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if err := fill(f); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// applyDirs sets final directory metadata bottom-up (children before
// parents, so a read-only directory cannot block its own contents) and
// fsyncs every directory.
func applyDirs(p *plan) error {
	chown := copyx.IsRoot()
	for i := len(p.entries) - 1; i >= 0; i-- {
		e := p.entries[i]
		if e.Type != manifest.TypeDir {
			continue
		}
		dir := filepath.Join(p.partialDir, filepath.FromSlash(e.Path))
		if err := state.SyncDir(dir); err != nil {
			return fmt.Errorf("sync %s: %w", e.Path, err)
		}
		if err := copyx.Apply(dir, metaOf(e), chown); err != nil {
			return err
		}
	}
	return nil
}
