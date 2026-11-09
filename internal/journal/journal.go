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

// line is the on-disk record: kind "h" is a header, "f" a finished file.
type line struct {
	Kind  string `json:"t"`
	Job   string `json:"job,omitempty"`
	RunID string `json:"run_id,omitempty"`
	Entry
}

// Journal appends records to the journal file. It is safe for concurrent use.
type Journal struct {
	mu sync.Mutex
	f  *os.File
}

// Open opens (creating if needed) the journal at path and appends a header
// naming the job and run. A torn final line from a crash is terminated first
// so it cannot swallow the header.
func Open(path, job, runID string) (*Journal, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	j := &Journal{f: f}
	if err := j.terminateTornLine(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := j.write(line{Kind: "h", Job: job, RunID: runID}); err != nil {
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
}

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
	rep := Replay{Entries: map[string]Entry{}}
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
	case "f":
		if l.Path == "" || len(l.SHA256) != 64 {
			rep.Bad++
			return nil
		}
		rep.Entries[l.Path] = l.Entry
	default:
		rep.Bad++
	}
	return nil
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
