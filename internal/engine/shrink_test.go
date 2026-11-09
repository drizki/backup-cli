package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/retention"
)

// emptySource simulates an unmounted share: the mountpoint is an empty dir.
func (f *fixture) emptySource() {
	f.t.Helper()
	items, err := os.ReadDir(f.src)
	if err != nil {
		f.t.Fatal(err)
	}
	hidden := filepath.Join(f.root, "share-contents-while-unmounted")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		f.t.Fatal(err)
	}
	for _, it := range items {
		if err := os.Rename(filepath.Join(f.src, it.Name()), filepath.Join(hidden, it.Name())); err != nil {
			f.t.Fatal(err)
		}
	}
}

func dataFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		switch filepath.Base(p) {
		case "MANIFEST.sha256", "MANIFEST.tsv", ".backup-cli.json":
		default:
			n++
		}
		return nil
	})
	return n
}

func TestEmptySourceIsRefusedAndKeepsEveryGoodBackup(t *testing.T) {
	f := newFixture(t)
	f.j.Keep = 2
	f.j.OnFailure = "touch " + filepath.Join(f.root, "hook-fired")
	f.save()
	f.e.Retention = retention.Func(f.store.Dir)
	f.mustRun()
	f.mustRun()
	before := len(f.committed())
	f.emptySource()
	for i := 0; i < 2; i++ {
		err := f.run()
		f.expectCode(exitcode.Refused, err)
		if !strings.Contains(err.Error(), "may not be mounted") {
			t.Fatalf("message does not mention the mount: %v", err)
		}
	}
	got := f.committed()
	if len(got) != before {
		t.Fatalf("committed backups: %d, want %d", len(got), before)
	}
	for _, b := range got {
		if dataFiles(t, b.Path) == 0 {
			t.Errorf("backup %s lost its data", b.Path)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "hook-fired")); err != nil {
		t.Errorf("on-failure hook did not fire for the refused run: %v", err)
	}
}

func TestEmptySourceDoesNotSweepAResumedPartial(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(3)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	part := f.partials()[0]
	before := dataFiles(t, part)
	f.e.AfterCopy = nil
	f.emptySource()
	f.expectCode(exitcode.Refused, f.run())
	if after := dataFiles(t, part); after != before {
		t.Fatalf("the partial held %d files and now holds %d", before, after)
	}
}

func TestForceAllowsAShrunkenSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	f.emptySource()
	f.mustRun(func(r *cli.EngineRequest) { r.Force = true })
	if n := len(f.committed()); n != 2 {
		t.Fatalf("committed backups: %d, want 2", n)
	}
}

func TestSourceAtHalfTheFilesIsAccepted(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	files := dataFiles(t, f.latest())
	removed := 0
	_ = filepath.WalkDir(f.src, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && removed < files/2 {
			removed++
			_ = os.Remove(p)
		}
		return nil
	})
	f.mustRun()
}

// cliMust runs the real CLI and fails the test unless it exits with want.
func cliMust(t *testing.T, stateDir string, want int, args ...string) {
	t.Helper()
	if code, out, errb := cliRun(t, stateDir, args...); code != want {
		t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errb)
	}
}

func TestEmptySourceThroughTheCLIKeepsBothBackups(t *testing.T) {
	root := t.TempDir()
	src, dst, stateDir := filepath.Join(root, "src"), filepath.Join(root, "bk"), filepath.Join(root, "state")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(src, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cliMust(t, stateDir, 0, "add", src, filepath.Join(dst, "e-{timestamp}"), "--no-schedule", "--name", "e", "--keep", "2")
	for i := 0; i < 2; i++ {
		cliMust(t, stateDir, 0, "run", "e")
		time.Sleep(1100 * time.Millisecond) // timestamps have one-second resolution
	}
	for _, n := range []string{"a", "b", "c"} {
		if err := os.Remove(filepath.Join(src, n)); err != nil {
			t.Fatal(err)
		}
	}
	cliMust(t, stateDir, exitcode.Refused, "run", "e")
	cliMust(t, stateDir, exitcode.Refused, "run", "e")
	dirs, err := filepath.Glob(filepath.Join(dst, "e-*"))
	if err != nil || len(dirs) != 2 {
		t.Fatalf("backups left: %v (%v), want 2", dirs, err)
	}
	for _, d := range dirs {
		if n := dataFiles(t, d); n != 3 {
			t.Errorf("%s holds %d data files, want 3", d, n)
		}
	}
}
