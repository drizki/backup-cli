// Package journal is the append-only record of files already completed inside
// a ".partial" backup directory. An entry is appended only after the file's
// data and metadata are durable, so a resumed run can trust (and re-verify)
// exactly the files named here.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// Name is the journal file name inside the partial directory.
const Name = ".backup-cli-partial.jsonl"

// ErrForeign means the journal belongs to a different job.
var ErrForeign = errors.New("journal belongs to a different job")

// Entry records one completed file.
type Entry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	MtimeNS int64  `json:"mtime_ns"`
	SHA256  string `json:"sha256"`
}

// line is the on-disk record: kind "h" is a header (a session of a run
// starts), "f" a finished file and "s" a clean stop of the session.
type line struct {
	Kind      string `json:"t"`
	Job       string `json:"job,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	StartedNS int64  `json:"started_ns,omitempty"`
	Entry
}

// Journal appends records to the journal file. It is safe for concurrent use.
type Journal struct {
	mu sync.Mutex
	f  *os.File
}

// Open opens (creating if needed) the journal at path and appends a header
// naming the job, the run and the time the run started. A torn final line
// from a crash is terminated first so it cannot swallow the header.
func Open(path, job, runID string, started time.Time) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	j := &Journal{f: f}
	var startedNS int64
	if !started.IsZero() {
		startedNS = started.UnixNano()
	}
	if err := j.terminateTornLine(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := j.write(line{Kind: "h", Job: job, RunID: runID, StartedNS: startedNS}); err != nil {
		_ = f.Close()
		return nil, err
	}
	return j, nil
}

func (j *Journal) terminateTornLine() error {
	fi, err := j.f.Stat()
	if err != nil || fi.Size() == 0 {
		return err
	}
	last := make([]byte, 1)
	if _, err := j.f.ReadAt(last, fi.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = j.f.Write([]byte("\n"))
	return err
}

func (j *Journal) write(l line) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, err = j.f.Write(append(b, '\n'))
	return err
}

// Add records a completed file.
func (j *Journal) Add(e Entry) error { return j.write(line{Kind: "f", Entry: e}) }

// CleanStop records that this session stopped on purpose (deadline or
// signal) after every finished file was journaled, and syncs the journal. A
// later run may then trust the session's entries without re-hashing them.
func (j *Journal) CleanStop() error {
	if err := j.write(line{Kind: "s"}); err != nil {
		return err
	}
	return j.Sync()
}

// Sync flushes the journal to disk.
func (j *Journal) Sync() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.f.Sync()
}

// Close syncs and closes the journal.
func (j *Journal) Close() error {
	serr := j.Sync()
	if err := j.f.Close(); err != nil {
		return err
	}
	return serr
}

// Replay is the parsed content of a journal.
type Replay struct {
	Job     string
	Entries map[string]Entry // latest entry per path
	Bad     int              // unparsable lines that were skipped
	// Started is the earliest run start recorded in a header; zero if none.
	Started time.Time

	session string            // run id of the latest header while parsing
	owner   map[string]string // run id of the session that wrote each entry
	stopped map[string]bool   // sessions that ended with a clean stop
}

// Trusted reports whether the entry for path was written by a session that
// later stopped cleanly. Such a file was fsynced by this tool before it was
// journaled, so a resume need not re-hash it. An entry from a session that
// was killed (or lost power) is not trusted.
func (rep Replay) Trusted(path string) bool { return rep.stopped[rep.owner[path]] }

// Read parses the journal at path, tolerating torn or corrupt lines. A
// missing file yields an error satisfying errors.Is(err, os.ErrNotExist);
// headers naming different jobs yield ErrForeign.
func Read(path string) (Replay, error) {
	f, err := os.Open(path)
	if err != nil {
		return Replay{}, err
	}
	defer func() { _ = f.Close() }()
	return parse(f)
}

func parse(r io.Reader) (Replay, error) {
	rep := Replay{Entries: map[string]Entry{}, owner: map[string]string{}, stopped: map[string]bool{}}
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			if perr := rep.apply(raw); perr != nil {
				return Replay{}, perr
			}
		}
		if err == io.EOF {
			return rep, nil
		}
		if err != nil {
			return Replay{}, err
		}
	}
}

func (rep *Replay) apply(raw []byte) error {
	var l line
	if err := json.Unmarshal(raw, &l); err != nil {
		rep.Bad++
		return nil
	}
	switch l.Kind {
	case "h":
		if rep.Job != "" && l.Job != rep.Job {
			return fmt.Errorf("%w: %q and %q", ErrForeign, rep.Job, l.Job)
		}
		rep.Job = l.Job
		rep.session = l.RunID
		rep.noteStart(l.StartedNS)
	case "s":
		rep.stopped[rep.session] = true
	case "f":
		if l.Path == "" || len(l.SHA256) != 64 {
			rep.Bad++
			return nil
		}
		rep.Entries[l.Path] = l.Entry
		rep.owner[l.Path] = rep.session
	default:
		rep.Bad++
	}
	return nil
}

func (rep *Replay) noteStart(ns int64) {
	if ns == 0 {
		return
	}
	if t := time.Unix(0, ns); rep.Started.IsZero() || t.Before(rep.Started) {
		rep.Started = t
	}
}

// Discard removes the journal file at path. It deletes only a regular file
// with the journal's own name, and is called once, after a backup is complete
// and its manifests and marker are durable.
func Discard(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return os.Remove(path)
}
