package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/lock"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/runlog"
	"github.com/drizki/backup-cli/internal/state"
)

func (f *fixture) expectRefused(err error, reasonPart string) {
	f.t.Helper()
	f.expectCode(exitcode.Refused, err)
	if !strings.Contains(err.Error(), reasonPart) {
		f.t.Fatalf("reason %q does not mention %q", err, reasonPart)
	}
	st := f.lastStatus()
	if st.State != state.StateRefused || st.ExitCode != exitcode.Refused || !strings.Contains(st.RefuseReason, reasonPart) {
		f.t.Fatalf("status %+v", st)
	}
	if len(f.committed()) != 0 {
		f.t.Fatal("a refused run committed a backup")
	}
}

func TestLockContentionIsRefused(t *testing.T) {
	f := newFixture(t)
	if err := f.store.Ensure(); err != nil {
		t.Fatal(err)
	}
	held, err := lock.Acquire(f.store.LockPath("docs")) // a separate open file description
	if err != nil {
		t.Fatal(err)
	}
	f.expectRefused(f.run(), "another run of job")
	if len(f.partials()) != 0 {
		t.Error("a refused run created a .partial")
	}
	_ = held.Close()
	f.mustRun() // the lock is free again
	if _, err := os.Stat(f.store.LockPath("docs")); err != nil {
		t.Error("lock file must be kept")
	}
}

func TestOverlapIsRefused(t *testing.T) {
	f := newFixture(t)
	f.j.DstTemplate = filepath.Join(f.src, "inside", "docs-{timestamp}")
	f.save()
	f.expectRefused(f.run(), "inside the source")

	f2 := newFixture(t)
	link := filepath.Join(f2.root, "alias")
	if err := os.Symlink(f2.src, link); err != nil {
		t.Fatal(err)
	}
	f2.j.DstTemplate = filepath.Join(link, "docs-{timestamp}")
	f2.save()
	f2.expectRefused(f2.run(), "inside the source")

	f3 := newFixture(t) // the source lies inside one of the job's own backups
	f3.j.Source = filepath.Join(f3.dst, "docs-20260101T000000Z", "data")
	if err := os.MkdirAll(f3.j.Source, 0o755); err != nil {
		t.Fatal(err)
	}
	f3.save()
	f3.expectRefused(f3.run(), "inside the backup directory")
}

func TestSourceAndDestinationGuards(t *testing.T) {
	f := newFixture(t)
	f.j.Source = filepath.Join(f.root, "missing")
	f.save()
	f.expectRefused(f.run(), "not a readable directory")

	f = newFixture(t)
	f.j.DstTemplate = filepath.Join(f.root, "no-such-parent", "docs-{timestamp}")
	f.save()
	f.expectRefused(f.run(), "does not exist")

	if os.Geteuid() != 0 {
		f = newFixture(t)
		if err := os.Chmod(f.dst, 0o555); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(f.dst, 0o755) }()
		f.expectRefused(f.run(), "not writable")
	}
}

func TestFreeSpaceRefusal(t *testing.T) {
	f := newFixture(t)
	f.e.Statfs = func(string) (uint64, error) { return 1 << 20, nil }
	f.expectRefused(f.run(), "not enough free space")
	if !strings.Contains(f.lastStatus().RefuseReason, "need") {
		t.Error("the refusal must say how much space is needed")
	}
	if entries, _ := os.ReadDir(f.dst); len(entries) != 0 {
		t.Fatalf("destination touched: %v", entries)
	}
	f.e.Statfs = func(string) (uint64, error) { return 1 << 40, nil }
	f.mustRun()
	f.e.Statfs = func(string) (uint64, error) { return 0, os.ErrPermission }
	f.expectCode(exitcode.Failed, f.run())
}

func TestManifestNameCollisionIsRefused(t *testing.T) {
	for _, name := range []string{"MANIFEST.sha256", "MANIFEST.tsv", ".backup-cli.json"} {
		f := newFixture(t)
		f.put(name, []byte("user data"))
		f.expectRefused(f.run(), name)
		if b, _ := os.ReadFile(f.path(name)); string(b) != "user data" {
			t.Fatal("source file modified")
		}
	}
}

func TestExistingFinalDirectoryIsRefused(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	f.now = baseTime
	final := f.j.DstFor(f.now)
	if err := os.Mkdir(final, 0o755); err != nil {
		t.Fatal(err)
	}
	f.expectRefused(f.run(), "already exists")
	if entries, _ := os.ReadDir(final); len(entries) != 0 {
		t.Error("existing directory modified")
	}
}

func TestUnknownPartialDirectoriesAreNeverReused(t *testing.T) {
	cases := map[string]func(t *testing.T, f *fixture, dir string){
		"no journal": func(t *testing.T, _ *fixture, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "precious"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"other job": func(t *testing.T, _ *fixture, dir string) {
			jr, err := journal.Open(filepath.Join(dir, journal.Name), "someone-else", "r")
			if err != nil {
				t.Fatal(err)
			}
			_ = jr.Close()
		},
		"marker of other job": func(t *testing.T, _ *fixture, dir string) {
			if err := backupset.WriteMarker(dir, backupset.Marker{Job: "someone-else"}); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt journal": func(t *testing.T, _ *fixture, dir string) {
			a, _ := journal.Open(filepath.Join(dir, journal.Name), "x", "r")
			_ = a.Close()
			b, _ := journal.Open(filepath.Join(dir, journal.Name), "y", "r")
			_ = b.Close()
		},
	}
	for name, setup := range cases {
		f := newFixture(t)
		part := f.j.DstFor(baseTime.Add(-time.Hour)) + backupset.PartialSuffix
		if err := os.Mkdir(part, 0o755); err != nil {
			t.Fatal(err)
		}
		setup(t, f, part)
		before := snap(t, part)
		err := f.run()
		if exitcode.Code(err) != exitcode.Refused {
			t.Fatalf("%s: exit %d (%v)", name, exitcode.Code(err), err)
		}
		if d := diffSnaps(before, snap(t, part)); d != "" {
			t.Fatalf("%s: the unknown directory was modified:\n%s", name, d)
		}
	}
}

func TestPartialSymlinkIsRefused(t *testing.T) {
	f := newFixture(t)
	target := t.TempDir()
	part := f.j.DstFor(baseTime.Add(-time.Hour)) + backupset.PartialSuffix
	if err := os.Symlink(target, part); err != nil {
		t.Fatal(err)
	}
	f.expectCode(exitcode.Refused, f.run())
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatal("wrote through a symlinked partial directory")
	}
}

func TestPartialWithMarkerButNoJournalResumes(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(2)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	part := f.partials()[0]
	if err := journal.Discard(filepath.Join(part, journal.Name)); err != nil {
		t.Fatal(err)
	}
	if err := backupset.WriteMarker(part, backupset.Marker{Job: "docs"}); err != nil {
		t.Fatal(err)
	}
	f.e.AfterCopy = nil
	f.mustRun()
	f.sameAsSource(f.latest())
}

func TestBiweeklyGate(t *testing.T) {
	f := newFixture(t)
	f.j.Biweekly = true
	f.save()
	f.mustRun()
	before := f.committed()

	f.step = 24 * time.Hour
	if err := f.run(); err != nil { // skipped: exit 0
		t.Fatal(err)
	}
	st := f.lastStatus()
	if st.State != state.StateSkipped || st.SkipReason != "biweekly-gate" || st.ExitCode != 0 || !st.Terminal() {
		t.Fatalf("%+v", st)
	}
	if len(f.committed()) != len(before) {
		t.Fatal("a skipped run created a backup")
	}

	f.mustRun(func(r *cli.EngineRequest) { r.Force = true })
	if len(f.committed()) != 2 {
		t.Fatalf("--force did not bypass the gate: %d backups", len(f.committed()))
	}
	f.step = 14 * 24 * time.Hour
	f.mustRun()
	if len(f.committed()) != 3 || f.lastStatus().State != state.StateSucceeded {
		t.Fatal("the gate must open after 13 days")
	}
}

func TestVerifySampleFailureFailsTheRunAndSkipsRetention(t *testing.T) {
	f := newFixture(t)
	retained := 0
	f.e.Retention = func(context.Context, job.Job, []backupset.Backup, string, *runlog.Log) error { retained++; return nil }
	f.e.PostCommit = func(dir string) { flipByte(t, filepath.Join(dir, "sub", "b.bin")) }
	err := f.run(func(r *cli.EngineRequest) { r.VerifySample = 100 })
	f.expectCode(exitcode.Failed, err)
	st := f.lastStatus()
	if st.State != state.StateFailed || !st.VerifyFailed || retained != 0 {
		t.Fatalf("%+v retained=%d", st, retained)
	}
	if len(f.committed()) != 1 {
		t.Fatal("the backup must be kept, flagged by the failed run")
	}
	if !strings.Contains(f.out.String(), "sub/b.bin: checksum mismatch") {
		t.Errorf("log lacks the failure:\n%s", f.out.String())
	}
}

func TestVerifySampleDisabledAndJobDefault(t *testing.T) {
	f := newFixture(t)
	f.e.PostCommit = func(dir string) { flipByte(t, filepath.Join(dir, "sub", "b.bin")) }
	f.mustRun(func(r *cli.EngineRequest) { r.VerifySample = 0 })
	if !strings.Contains(f.out.String(), "post-commit verification is disabled") {
		t.Error("disabled verification not logged")
	}
	f2 := newFixture(t)
	f2.j.VerifySamplePct = 100
	f2.save()
	f2.e.PostCommit = f.e.PostCommit
	f2.expectCode(exitcode.Failed, f2.run())
}

func TestRetentionSeam(t *testing.T) {
	f := newFixture(t)
	var seen [][]string
	f.e.Retention = func(_ context.Context, j job.Job, c []backupset.Backup, _ string, _ *runlog.Log) error {
		var names []string
		for _, b := range c {
			names = append(names, b.Name)
		}
		seen = append(seen, names)
		return os.ErrPermission // a retention failure is only a warning
	}
	f.mustRun()
	f.mustRun()
	if len(seen) != 2 || len(seen[0]) != 1 || len(seen[1]) != 2 {
		t.Fatalf("retention saw %v", seen)
	}
	if st := f.lastStatus(); st.State != state.StateSucceeded || !hasWarning(st, "retention failed") {
		t.Fatalf("%+v", st)
	}
	calls := len(seen)

	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	f.j.Deadline = "1ns"
	f.save()
	_ = f.run()
	if len(seen) != calls {
		t.Fatal("retention ran after a dry run or a partial run")
	}
}

func TestManifestPathsFromPoisonedPreviousBackupAreRejected(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	prev := f.latest()
	tsv := filepath.Join(prev, manifest.TSVName)
	b, _ := os.ReadFile(tsv)
	poisoned := []byte(strings.Replace(string(b), "a.txt", "../../a.txt", 1))
	if err := os.WriteFile(tsv, poisoned, 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := backupset.ReadMarker(prev) // a valid marker for the poisoned manifest
	sum := sha256.Sum256(poisoned)
	m.ManifestSHA256 = hex.EncodeToString(sum[:])
	if err := backupset.WriteMarker(prev, m); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	st := f.lastStatus()
	if !hasWarning(st, "unsafe path") || st.FilesCopiedFromSource != 6 {
		t.Fatalf("%+v", st)
	}
	f.sameAsSource(f.latest())
}
