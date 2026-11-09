package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
)

// rewriteInPlace replaces a.txt with data of the same size and gives it the
// mtime, which is what a write within the same timestamp tick leaves behind on
// a coarse filesystem.
func (f *fixture) rewriteInPlace(data []byte, mtime time.Time) {
	f.t.Helper()
	if err := os.WriteFile(f.path("a.txt"), data, 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chtimes(f.path("a.txt"), mtime, mtime); err != nil {
		f.t.Fatal(err)
	}
}

func TestMarkerRecordsTheRunStart(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	m, err := backupset.ReadMarker(f.latest())
	if err != nil || !m.Started.Equal(f.now) {
		t.Fatalf("marker started %v (%v), want %v", m.Started, err, f.now)
	}
}

func TestFileRewrittenInTheSameTickAsTheRunIsCopiedAgain(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	f.now = baseTime
	tick := baseTime.Add(-500 * time.Millisecond) // inside the margin before the run start
	f.rewriteInPlace([]byte("alpha"), tick)
	f.mustRun()
	f.rewriteInPlace([]byte("OMEGA"), tick)
	f.step = time.Hour
	f.mustRun()
	if b, _ := os.ReadFile(filepath.Join(f.latest(), "a.txt")); string(b) != "OMEGA" {
		t.Fatalf("the second backup holds %q", b)
	}
	f.sameAsSource(f.latest())
}

func TestFileOlderThanTheRunStaysSeededFromThePreviousBackup(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	f.mustRun()
	if st := f.lastStatus(); st.FilesCopiedFromSource != 0 || st.FilesCopiedFromPrev != 6 {
		t.Fatalf("%+v", st)
	}
}

func TestResumeCopiesAJournaledFileRewrittenInTheSameTick(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	f.now = baseTime
	tick := baseTime.Add(-500 * time.Millisecond)
	f.rewriteInPlace([]byte("alpha"), tick)
	ctx := f.interruptAfter(1) // a.txt is the first file copied
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	f.e.AfterCopy = nil
	f.rewriteInPlace([]byte("OMEGA"), tick)
	f.step = time.Hour
	f.mustRun()
	if b, _ := os.ReadFile(filepath.Join(f.latest(), "a.txt")); string(b) != "OMEGA" {
		t.Fatalf("the resumed backup holds %q", b)
	}
}
