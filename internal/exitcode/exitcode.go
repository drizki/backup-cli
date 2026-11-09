// Package exitcode defines the process exit codes and maps errors onto them.
package exitcode

import (
	"errors"
	"fmt"
)

// Exit codes. They are part of the public contract.
const (
	OK      = 0 // success (including a skipped biweekly run)
	Failed  = 1 // I/O error, verification failure, unreadable file
	Usage   = 2 // bad flags, arguments or configuration
	Partial = 3 // deadline reached, .partial kept
	Refused = 4 // a safety guard refused to proceed
)

// Error carries an explicit exit code alongside the underlying error.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// With wraps err with the given exit code. A nil err yields nil.
func With(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// Usagef builds a usage error (exit 2).
func Usagef(format string, a ...any) error {
	return &Error{Code: Usage, Err: fmt.Errorf(format, a...)}
}

// Refusedf builds a refusal error (exit 4).
func Refusedf(format string, a ...any) error {
	return &Error{Code: Refused, Err: fmt.Errorf(format, a...)}
}

// Failedf builds a failure error (exit 1).
func Failedf(format string, a ...any) error {
	return &Error{Code: Failed, Err: fmt.Errorf(format, a...)}
}

// Code maps an error to an exit code. nil is OK; an error carrying an
// *Error anywhere in its chain uses that code; anything else is Failed.
func Code(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Failed
}

// Name returns a short label for a code, used in docs and status output.
func Name(code int) string {
	switch code {
	case OK:
		return "ok"
	case Failed:
		return "failed"
	case Usage:
		return "usage"
	case Partial:
		return "partial"
	case Refused:
		return "refused"
	}
	return "unknown"
}
