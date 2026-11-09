// Package runlog writes the timestamped run.log and mirrors every line to a
// second writer (stdout, so journald sees it under systemd).
package runlog

import (
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// Log is safe for concurrent use. A Log without a file only mirrors.
type Log struct {
	mu   sync.Mutex
	f    *os.File
	tee  io.Writer
	Now  func() time.Time
	errs int
}

// Open appends to the log file at path and mirrors to tee (may be nil).
func Open(path string, tee io.Writer) (*Log, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, err
	}
	return &Log{f: f, tee: tee, Now: time.Now}, nil
}

// Mirror returns a Log that writes nowhere but tee (dry runs).
func Mirror(tee io.Writer) *Log { return &Log{tee: tee, Now: time.Now} }

func (l *Log) emit(level, format string, a ...any) {
	ts := l.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	line := fmt.Sprintf("%s %-5s %s\n", ts, level, fmt.Sprintf(format, a...))
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		if _, err := l.f.WriteString(line); err != nil {
			l.errs++
		}
	}
	if l.tee != nil {
		_, _ = io.WriteString(l.tee, line)
	}
}

// Infof, Warnf and Errorf log at their level.
func (l *Log) Infof(format string, a ...any)  { l.emit("INFO", format, a...) }
func (l *Log) Warnf(format string, a ...any)  { l.emit("WARN", format, a...) }
func (l *Log) Errorf(format string, a ...any) { l.emit("ERROR", format, a...) }

// WriteErrors is the number of failed log-file writes.
func (l *Log) WriteErrors() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.errs
}

// Close closes the log file.
func (l *Log) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
