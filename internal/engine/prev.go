package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/manifest"
)

// prevBackup is the newest committed backup, used to seed unchanged files.
type prevBackup struct {
	dir     string
	marker  backupset.Marker
	entries map[string]prevEntry
}

// prevEntry is what a run needs to know about one object of the previous
// backup. It is deliberately small: the map holds one per file of the tree.
type prevEntry struct {
	size, mtimeNS int64
	sum           [sha256.Size]byte // valid when file is true
	file          bool
}

// sumHex returns the recorded checksum in the manifest's hex form.
func (pe prevEntry) sumHex() string { return hex.EncodeToString(pe.sum[:]) }

// loadPrev reads the newest committed backup's manifest. Any problem is a
// warning: the run then reads everything from the source. The manifest is
// streamed into compact entries with their own copy of each path, so no
// manifest line stays in memory.
func (r *run) loadPrev() *prevBackup {
	committed := backupset.OnlyCommitted(r.backups)
	if len(committed) == 0 {
		return nil
	}
	b := committed[len(committed)-1]
	f, err := os.Open(filepath.Join(b.Path, manifest.TSVName))
	if err != nil {
		r.warn("previous backup %s is unusable (%v); reading everything from the source", b.Name, err)
		return nil
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	tee := io.TeeReader(f, h)
	m := make(map[string]prevEntry, max(b.Marker.FileCount, 0))
	err = manifest.EachTSV(tee, func(e manifest.Entry) error {
		pe := prevEntry{size: e.Size, mtimeNS: e.MtimeNS}
		if e.Type == manifest.TypeFile {
			if _, err := hex.Decode(pe.sum[:], []byte(e.SHA256)); err != nil {
				return err
			}
			pe.file = true
		}
		m[strings.Clone(e.Path)] = pe
		return nil
	})
	drain(tee) // the checksum covers the whole file even after a parse error
	if hex.EncodeToString(h.Sum(nil)) != b.Marker.ManifestSHA256 {
		r.warn("previous backup %s: manifest does not match its marker; reading everything from the source", b.Name)
		return nil
	}
	if err != nil {
		r.warn("previous backup %s: %v; reading everything from the source", b.Name, err)
		return nil
	}
	return &prevBackup{dir: b.Path, marker: *b.Marker, entries: m}
}

// lookup returns the previous backup's entry for path; safe on nil.
func (pb *prevBackup) lookup(path string) (prevEntry, bool) {
	if pb == nil {
		return prevEntry{}, false
	}
	pe, ok := pb.entries[path]
	return pe, ok
}

// drain reads r to its end.
func drain(r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		if _, err := r.Read(buf); err != nil {
			return
		}
	}
}
