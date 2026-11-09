// Package backupset reads and writes backup markers and discovers the
// backups of a job inside its destination parent directory.
//
// A directory is a committed backup only when it carries a valid marker
// (.backup-cli.json) that names the owning job. Marker-less directories,
// ".partial" directories and directories with unreadable markers are never
// treated as committed backups.
package backupset

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/state"
)

// MarkerName is the marker file, written last before the final rename.
const MarkerName = ".backup-cli.json"

// PartialSuffix marks an in-progress backup directory.
const PartialSuffix = ".partial"

// MarkerFormat is the current marker format version.
const MarkerFormat = 1

// Marker is the content of .backup-cli.json.
type Marker struct {
	Format         int       `json:"format"`
	Job            string    `json:"job"`
	RunID          string    `json:"run_id"`
	ToolVersion    string    `json:"tool_version"`
	Created        time.Time `json:"created"`
	Started        time.Time `json:"started,omitzero"` // when the run began; the first run start for a resumed backup
	Source         string    `json:"source"`
	FileCount      int64     `json:"file_count"`
	Bytes          int64     `json:"bytes"`
	ManifestSHA256 string    `json:"manifest_sha256"`
}

// Status classifies a candidate directory.
type Status string

// Backup classifications.
const (
	Committed Status = "committed"
	Partial   Status = "partial"
	NoMarker  Status = "no-marker"
	BadMarker Status = "bad-marker"
	Foreign   Status = "foreign" // valid marker, but owned by another job
)

// Backup describes one candidate directory in a job's destination parent.
type Backup struct {
	Name   string  `json:"name"`
	Path   string  `json:"path"`
	Job    string  `json:"job"`
	Status Status  `json:"status"`
	Marker *Marker `json:"marker,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

// ID is the "<job>/<name>" identifier shown by `list backups`.
func (b Backup) ID() string { return b.Job + "/" + b.Name }

// IsCommitted reports whether the backup is committed.
func (b Backup) IsCommitted() bool { return b.Status == Committed }

// ReadMarker loads and validates the marker of dir.
func ReadMarker(dir string) (Marker, error) {
	var m Marker
	b, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return Marker{}, fmt.Errorf("marker is not valid JSON: %w", err)
	}
	if m.Format != MarkerFormat {
		return Marker{}, fmt.Errorf("unsupported marker format %d", m.Format)
	}
	if m.Job == "" {
		return Marker{}, errors.New("marker has no job")
	}
	return m, nil
}

// WriteMarker atomically writes the marker into dir.
func WriteMarker(dir string, m Marker) error {
	if m.Format == 0 {
		m.Format = MarkerFormat
	}
	return state.WriteJSON(filepath.Join(dir, MarkerName), m)
}

func classify(dir, jobName string) Backup {
	b := Backup{Name: filepath.Base(dir), Path: dir, Job: jobName}
	if strings.HasSuffix(b.Name, PartialSuffix) {
		b.Status = Partial
		return b
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() { // symlinks are not directories under Lstat
		b.Status = NoMarker
		b.Reason = "not a directory"
		return b
	}
	m, err := ReadMarker(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b.Status = NoMarker
	case err != nil:
		b.Status = BadMarker
		b.Reason = err.Error()
	case m.Job != jobName:
		b.Status = Foreign
		b.Marker = &m
		b.Reason = fmt.Sprintf("marker belongs to job %q", m.Job)
	default:
		b.Status = Committed
		b.Marker = &m
	}
	return b
}

// Scan lists every directory in the job's destination parent whose name
// matches the job's template (including its ".partial" form), classified and
// ordered oldest first (by marker creation time, then name). Directories that
// do not match the template are ignored entirely. A missing parent yields an
// empty result.
func Scan(j job.Job) ([]Backup, error) {
	entries, err := os.ReadDir(j.DstParent())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		name := e.Name()
		base := strings.TrimSuffix(name, PartialSuffix)
		if !j.MatchesName(base) {
			continue
		}
		out = append(out, classify(filepath.Join(j.DstParent(), name), j.Name))
	}
	sort.SliceStable(out, func(a, b int) bool {
		ta, tb := created(out[a]), created(out[b])
		if !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return out[a].Name < out[b].Name
	})
	return out, nil
}

// OnlyCommitted filters a scan down to committed backups, preserving order.
func OnlyCommitted(all []Backup) []Backup {
	var out []Backup
	for _, b := range all {
		if b.IsCommitted() {
			out = append(out, b)
		}
	}
	return out
}

func created(b Backup) time.Time {
	if b.Marker != nil {
		return b.Marker.Created
	}
	return time.Time{}
}
