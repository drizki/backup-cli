// Package state manages the plain-file state directory: job definitions and
// run status files. Everything is JSON, readable with cat and jq.
//
//	jobs/<job>.json
//	runs/<run-id>/status.json
//	runs/<run-id>/run.log
//	locks/<job>.lock
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/drizki/backup-cli/internal/job"
)

// DefaultDir is the state directory used when none is configured.
const DefaultDir = "/var/lib/backup-cli"

// ErrNotFound is returned when a job or run does not exist.
var ErrNotFound = errors.New("not found")

// Run states.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StatePartial   = "partial"
	StateFailed    = "failed"
	StateRefused   = "refused"
	StateSkipped   = "skipped"
)

// Store is a handle on a state directory.
type Store struct{ Dir string }

// New returns a Store rooted at dir.
func New(dir string) Store { return Store{Dir: dir} }

// JobsDir, RunsDir and LocksDir return the layout directories.
func (s Store) JobsDir() string  { return filepath.Join(s.Dir, "jobs") }
func (s Store) RunsDir() string  { return filepath.Join(s.Dir, "runs") }
func (s Store) LocksDir() string { return filepath.Join(s.Dir, "locks") }

// JobPath is the file holding the named job.
func (s Store) JobPath(name string) string { return filepath.Join(s.JobsDir(), name+".json") }

// RunDir is the directory of one run.
func (s Store) RunDir(runID string) string { return filepath.Join(s.RunsDir(), runID) }

// StatusPath is the status.json of one run.
func (s Store) StatusPath(runID string) string { return filepath.Join(s.RunDir(runID), "status.json") }

// LockPath is the lock file of one job.
func (s Store) LockPath(name string) string { return filepath.Join(s.LocksDir(), name+".lock") }

// Ensure creates the state layout directories (mode 0755).
func (s Store) Ensure() error {
	for _, d := range []string{s.Dir, s.JobsDir(), s.RunsDir(), s.LocksDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create state dir: %w", err)
		}
	}
	return nil
}

// SaveJob validates and atomically writes the job file.
func (s Store) SaveJob(j job.Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.JobsDir(), 0o755); err != nil {
		return err
	}
	return WriteJSON(s.JobPath(j.Name), j)
}

// LoadJob reads a job by name.
func (s Store) LoadJob(name string) (job.Job, error) {
	var j job.Job
	if !job.ValidName(name) {
		return j, fmt.Errorf("invalid job name %q", name)
	}
	b, err := os.ReadFile(s.JobPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return j, fmt.Errorf("job %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(b, &j); err != nil {
		return j, fmt.Errorf("job %q: corrupt job file: %w", name, err)
	}
	if j.DstTemplate != "" {
		j.DstTemplate = filepath.Clean(j.DstTemplate) // a hand-edited job must compare equal to Scan's cleaned paths
	}
	return j, nil
}

// ListJobs returns all jobs sorted by name. Unreadable job files are
// returned in the error list but do not stop the listing.
func (s Store) ListJobs() ([]job.Job, []error) {
	entries, err := os.ReadDir(s.JobsDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var jobs []job.Job
	var errs []error
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || filepath.Ext(n) != ".json" {
			continue
		}
		name := n[:len(n)-len(".json")]
		if !job.ValidName(name) {
			continue
		}
		j, err := s.LoadJob(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		jobs = append(jobs, j)
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].Name < jobs[b].Name })
	return jobs, errs
}

// RemoveJob deletes the job definition file only (never any backup data).
func (s Store) RemoveJob(name string) error {
	if !job.ValidName(name) {
		return fmt.Errorf("invalid job name %q", name)
	}
	err := os.Remove(s.JobPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("job %q: %w", name, ErrNotFound)
	}
	return err
}

// Status is runs/<run-id>/status.json.
type Status struct {
	RunID                 string    `json:"run_id"`
	Job                   string    `json:"job"`
	State                 string    `json:"state"`
	Started               time.Time `json:"started"`
	Finished              time.Time `json:"finished,omitzero"`
	PID                   int       `json:"pid"`
	ToolVersion           string    `json:"tool_version"`
	Source                string    `json:"source"`
	Dest                  string    `json:"dest"`
	PartialDir            string    `json:"partial_dir"`
	FinalDir              string    `json:"final_dir"`
	FilesTotal            int64     `json:"files_total"`
	FilesCopiedFromSource int64     `json:"files_copied_from_source"`
	FilesCopiedFromPrev   int64     `json:"files_copied_from_prev"`
	BytesTotal            int64     `json:"bytes_total"`
	BytesFromSource       int64     `json:"bytes_from_source"`
	Warnings              []string  `json:"warnings"`
	Errors                []string  `json:"errors"`
	ExitCode              int       `json:"exit_code"`
	RefuseReason          string    `json:"refuse_reason"`
	DryRun                bool      `json:"dry_run"`
	Log                   string    `json:"log"`
	VerifyFailed          bool      `json:"verify_failed,omitempty"`
	SkipReason            string    `json:"skip_reason,omitempty"`
}

// Terminal reports whether the run state is final.
func (st Status) Terminal() bool { return st.State != StateRunning && st.State != "" }

// WriteStatus atomically writes a run's status.json, creating its run dir.
func (s Store) WriteStatus(st Status) error {
	if st.RunID == "" {
		return errors.New("status without run id")
	}
	if err := os.MkdirAll(s.RunDir(st.RunID), 0o755); err != nil {
		return err
	}
	return WriteJSON(s.StatusPath(st.RunID), st)
}

// ReadStatus loads one run's status.
func (s Store) ReadStatus(runID string) (Status, error) {
	var st Status
	if runID == "" || filepath.Base(runID) != runID || runID == "." || runID == ".." {
		return st, fmt.Errorf("invalid run id %q", runID)
	}
	b, err := os.ReadFile(s.StatusPath(runID))
	if errors.Is(err, fs.ErrNotExist) {
		return st, fmt.Errorf("run %q: %w", runID, ErrNotFound)
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("run %q: corrupt status: %w", runID, err)
	}
	return st, nil
}

// ListRuns returns all readable run statuses, newest first. A non-empty
// jobName restricts the result to that job.
func (s Store) ListRuns(jobName string) ([]Status, []error) {
	entries, err := os.ReadDir(s.RunsDir())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var runs []Status
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := s.ReadStatus(e.Name())
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				errs = append(errs, err)
			}
			continue
		}
		if jobName != "" && st.Job != jobName {
			continue
		}
		runs = append(runs, st)
	}
	sort.Slice(runs, func(a, b int) bool { return runs[a].RunID > runs[b].RunID })
	return runs, errs
}

// NewRunID builds a sortable run id: <UTC yyyymmddThhmmssZ>-<job>-<4 hex>.
func NewRunID(t time.Time, jobName string) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s-%s", t.UTC().Format(job.TimestampLayout), jobName, hex.EncodeToString(b[:]))
}

// WriteJSON atomically writes v as indented JSON (tmp file, fsync, rename,
// parent fsync) with mode 0644.
func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(b, '\n'), 0o644)
}

// WriteFileAtomic writes data to path via a temp file in the same directory.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(dir, "."+filepath.Base(path)+".tmp-"+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return SyncDir(dir)
}

// SyncDir fsyncs a directory so a rename inside it is durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
