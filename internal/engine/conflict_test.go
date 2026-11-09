package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
)

func TestMissingPreviousCopyFallsBackToTheSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	first := f.latest()
	if err := os.Remove(filepath.Join(first, "ünï", "cödé.txt")); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	st := f.lastStatus()
	if !hasWarning(st, "previous copy of ünï/cödé.txt cannot be read") || st.FilesCopiedFromSource != 1 {
		t.Fatalf("%+v", st)
	}
	f.sameAsSource(f.latest())
}

func TestPreviousCopyReplacedBySymlinkIsNotFollowed(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	first := f.latest()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(first, "a.txt")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, p); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	if st := f.lastStatus(); !hasWarning(st, "a.txt") || st.FilesCopiedFromSource != 1 {
		t.Fatalf("%+v", st)
	}
	f.sameAsSource(f.latest())
}

func interrupted(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(2)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	f.e.AfterCopy = nil
	f.step = time.Second // later runs must sort after the interrupted one
	return f, f.partials()[0]
}

func TestResumeReplacesAStaleSymlinkAtomically(t *testing.T) {
	f, part := interrupted(t)
	stale := filepath.Join(part, "link-rel")
	_ = os.Remove(stale)
	if err := os.Symlink("somewhere/else", stale); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	f.sameAsSource(f.latest())
}

func TestResumeReplacesAnObjectOfTheWrongType(t *testing.T) {
	f, part := interrupted(t)
	conflict := filepath.Join(part, "empty")
	if err := os.Remove(conflict); err != nil { // an empty directory made by the interrupted run
		t.Fatal(err)
	}
	if err := os.Symlink(f.root, conflict); err != nil { // a symlink where a directory belongs
		t.Fatal(err)
	}
	f.mustRun()
	f.sameAsSource(f.latest())
	if _, err := os.Stat(f.path("a.txt")); err != nil {
		t.Fatalf("the symlink's target was touched: %v", err)
	}
}

func TestStateDirProblemsFailTheRun(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.store.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.store.Dir, []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.expectCode(exitcode.Failed, f.run())
	if entries, _ := os.ReadDir(f.dst); len(entries) != 0 {
		t.Fatal("destination touched although the run could not be recorded")
	}
}

func TestSourceReadableCheck(t *testing.T) {
	if err := probeReadable(t.TempDir()); err != nil {
		t.Errorf("empty directory: %v", err)
	}
	if err := probeReadable(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing directory accepted")
	}
}
