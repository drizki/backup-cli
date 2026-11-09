package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
)

// interruptedWith returns a fixture whose first run was stopped after n
// source files, and the journaled paths of the partial.
func interruptedWith(t *testing.T, n int64) (*fixture, string, []string) {
	t.Helper()
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(n)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	f.e.AfterCopy = nil
	part := f.partials()[0]
	rep, err := journal.Read(filepath.Join(part, journal.Name))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for p := range rep.Entries {
		paths = append(paths, p)
	}
	return f, part, paths
}

func TestResumeDoesNotCommitLeftoversOfTheInterruptedRun(t *testing.T) {
	f, part, journaled := interruptedWith(t, 3)
	victim := journaled[0]
	if err := os.Remove(f.path(victim)); err != nil { // deleted from the source after being copied
		t.Fatal(err)
	}
	for rel, mk := range map[string]func(string) error{
		"stray-dir/inner.txt":                 func(p string) error { return os.MkdirAll(filepath.Dir(p), 0o755) },
		"link-rel.link-123":                   func(p string) error { return os.Symlink("a.txt", p) },
		".backup-cli.json.tmp-9":              func(p string) error { return os.WriteFile(p, []byte("{"), 0o644) },
		"MANIFEST.tsv":                        func(p string) error { return os.WriteFile(p, []byte("old"), 0o644) },
		"half-written-then-deleted-file.part": func(p string) error { return os.WriteFile(p, []byte("half"), 0o644) },
	} {
		p := filepath.Join(part, filepath.FromSlash(rel))
		if err := mk(p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rel, "inner") {
			if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.step = time.Hour
	f.mustRun()
	latest := f.latest()
	for _, gone := range []string{victim, "stray-dir", "link-rel.link-123", ".backup-cli.json.tmp-9", "half-written-then-deleted-file.part"} {
		if _, err := os.Lstat(filepath.Join(latest, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s is in the committed backup although the run did not plan it", gone)
		}
	}
	rep, err := manifest.Verify(context.Background(), latest, manifest.VerifyOptions{SamplePct: 100})
	if err != nil || !rep.OK() || len(rep.Warnings) != 0 {
		t.Fatalf("verify: %+v %v", rep, err)
	}
	f.sameAsSource(latest)
}

func TestResumeRecreatesPathsThatChangedType(t *testing.T) {
	for name, change := range map[string]func(f *fixture, rel string){
		"file became a directory": func(f *fixture, rel string) {
			_ = os.Remove(f.path(rel))
			f.put(rel+"/inner.txt", []byte("now a directory"))
		},
		"file became a symlink": func(f *fixture, rel string) {
			_ = os.Remove(f.path(rel))
			f.link(rel, "a.txt")
		},
	} {
		f, _, journaled := interruptedWith(t, 2)
		victim := journaled[0]
		change(f, victim)
		f.step = time.Hour
		for i := range 2 {
			if err := f.run(); err != nil {
				t.Fatalf("%s: run %d after %q changed type: code %d: %v", name, i+1, victim, exitcode.Code(err), err)
			}
		}
		f.sameAsSource(f.latest())
	}
}

func TestResumeRecreatesADirectoryThatBecameAFile(t *testing.T) {
	f, part, _ := interruptedWith(t, 4)
	var dirPart string
	for _, d := range []string{"sub", "ünï", "empty"} {
		if fi, err := os.Lstat(filepath.Join(part, d)); err == nil && fi.IsDir() {
			dirPart = d
			break
		}
	}
	if dirPart == "" {
		t.Fatal("the interrupted run made no directory")
	}
	if err := os.RemoveAll(f.path(dirPart)); err != nil {
		t.Fatal(err)
	}
	f.put(dirPart, []byte("now a file"))
	f.step = time.Hour
	f.mustRun()
	f.sameAsSource(f.latest())
}

func TestVanishedFileIsDroppedWithAWarning(t *testing.T) {
	f := newFixture(t)
	var once atomic.Bool
	f.e.AfterCopy = func(string) {
		if once.CompareAndSwap(false, true) {
			_ = os.Remove(f.path("sub/deep/c.txt"))
			_ = os.Remove(f.path("a.txt"))
			_ = os.Remove(f.path("name with space.txt"))
			f.mkdirSwap("ünï/cödé.txt") // replaced by a directory
		}
	}
	f.mustRun(func(r *cli.EngineRequest) { r.Jobs = 1 })
	st := f.lastStatus()
	if st.WarningsTotal < 3 || !hasWarning(st, "vanished") {
		t.Fatalf("expected vanish warnings: %+v", st.Warnings)
	}
	latest := f.latest()
	for _, gone := range []string{"a.txt", "sub/deep/c.txt", "name with space.txt"} {
		if _, err := os.Lstat(filepath.Join(latest, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s vanished from the source but is in the backup", gone)
		}
	}
	rep, err := manifest.Verify(context.Background(), latest, manifest.VerifyOptions{SamplePct: 100})
	if err != nil || !rep.OK() || len(rep.Warnings) != 0 {
		t.Fatalf("verify: %+v %v", rep, err)
	}
	f.mustRun() // the next run sees the new tree
	f.sameAsSource(f.latest())
}

// mkdirSwap replaces the file at rel with a directory.
func (f *fixture) mkdirSwap(rel string) {
	f.t.Helper()
	if err := os.Remove(f.path(rel)); err != nil {
		f.t.Fatal(err)
	}
	f.mkdir(rel)
}

func TestEmptyPartialLeftByAKillIsResumed(t *testing.T) {
	cases := map[string]func(t *testing.T, part string){
		"no journal": func(*testing.T, string) {},
		"empty journal": func(t *testing.T, part string) {
			if err := os.WriteFile(filepath.Join(part, journal.Name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"torn header": func(t *testing.T, part string) {
			if err := os.WriteFile(filepath.Join(part, journal.Name), []byte(`{"t":"h","job":"do`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		f := newFixture(t)
		part := f.j.DstFor(baseTime.Add(-time.Hour)) + backupset.PartialSuffix
		if err := os.Mkdir(part, 0o700); err != nil {
			t.Fatal(err)
		}
		setup(t, part)
		if err := f.run(); err != nil {
			t.Fatalf("%s: code %d: %v", name, exitcode.Code(err), err)
		}
		if got := f.latest(); got != strings.TrimSuffix(part, backupset.PartialSuffix) {
			t.Errorf("%s: resumed into %s", name, got)
		}
		f.sameAsSource(f.latest())
	}
}

func TestHeaderlessJournalNextToDataIsNotResumed(t *testing.T) {
	f := newFixture(t)
	part := f.j.DstFor(baseTime.Add(-time.Hour)) + backupset.PartialSuffix
	if err := os.Mkdir(part, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(part, journal.Name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(part, "precious"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.expectCode(exitcode.Refused, f.run())
	if b, err := os.ReadFile(filepath.Join(part, "precious")); err != nil || string(b) != "x" {
		t.Fatalf("unknown data touched: %q %v", b, err)
	}
}
