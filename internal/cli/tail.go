package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/state"
)

// tailPoll is how often a followed log is checked for new output.
var tailPoll = 250 * time.Millisecond

func tailCmd() *command {
	return &command{
		name: "tail", summary: "Follow a run's log until it finishes", usage: "tail <run-id>", nargs: 1,
		long: "Prints the whole log, then keeps following it while the run is active and returns once the run reaches a final state.",
		setup: func(a *app, _ *flagSet) runFunc {
			return func(args []string) error { return a.tail(args[0]) }
		},
	}
}

func (a *app) tail(runID string) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	st, err := store.ReadStatus(runID)
	if errors.Is(err, state.ErrNotFound) {
		return exitcode.With(exitcode.Usage, fmt.Errorf("no such run %q (see 'backup-cli list runs')", runID))
	}
	if err != nil {
		return exitcode.With(exitcode.Failed, err)
	}
	path := st.Log
	if path == "" {
		path = store.RunDir(runID) + "/run.log"
	}
	return follow(a.ctx, a.out(), path, func() (bool, error) { return runFinished(store, runID) })
}

// runFinished reports whether the run reached a final state. A run whose
// process has vanished while still marked running is reported as an error.
func runFinished(store state.Store, runID string) (bool, error) {
	st, err := store.ReadStatus(runID)
	if err != nil {
		return false, err
	}
	if st.Terminal() {
		return true, nil
	}
	if st.PID > 0 && errors.Is(syscall.Kill(st.PID, 0), syscall.ESRCH) {
		return true, fmt.Errorf("run %s is marked running but process %d no longer exists", runID, st.PID)
	}
	return false, nil
}

// follow copies the file to w and keeps copying appended data until done
// reports true, after one last drain.
func follow(ctx context.Context, w io.Writer, path string, done func() (bool, error)) error {
	f, err := os.Open(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return exitcode.With(exitcode.Failed, err)
	}
	if f != nil {
		defer func() { _ = f.Close() }()
	}
	for {
		finished, derr := done()
		if f == nil {
			f, _ = os.Open(path)
		}
		if f != nil {
			if _, err := copyAll(w, f); err != nil {
				return exitcode.With(exitcode.Failed, err)
			}
		}
		if derr != nil {
			return exitcode.With(exitcode.Failed, derr)
		}
		if finished {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tailPoll):
		}
	}
}

// copyAll writes everything currently readable from r to w.
func copyAll(w io.Writer, r io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
