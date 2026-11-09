// Package logx is a tiny leveled logger for CLI diagnostics.
package logx

import (
	"fmt"
	"io"
)

// Logger writes diagnostics to a writer. Debug output requires verbose.
type Logger struct {
	w       io.Writer
	verbose bool
}

// New returns a Logger. A nil writer discards output.
func New(w io.Writer, verbose bool) *Logger {
	if w == nil {
		w = io.Discard
	}
	return &Logger{w: w, verbose: verbose}
}

// Verbose reports whether debug output is enabled.
func (l *Logger) Verbose() bool { return l.verbose }

// Infof always prints.
func (l *Logger) Infof(format string, a ...any) {
	_, _ = fmt.Fprintf(l.w, format+"\n", a...)
}

// Debugf prints only in verbose mode.
func (l *Logger) Debugf(format string, a ...any) {
	if l.verbose {
		_, _ = fmt.Fprintf(l.w, "debug: "+format+"\n", a...)
	}
}
