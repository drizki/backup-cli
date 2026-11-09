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
	"github.com/drizki/backup-cli/internal/state"
)

// interrupt cancels the context after n source files were copied.
func (f *fixture) interruptAfter(n int64) context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int64
	f.e.AfterCopy = func(string) {
		if count.Add(1) == n {
			cancel()
		}
	}
	return ctx
}

func TestInterruptedRunResumesToTheSameResultAsACleanRun(t *testing.T) {
	clean := newFixture(t)
	clean.mustRun()
	want := snap(t, clean.latest(), backupMeta...)

	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(3)
	err := f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 })
	f.expectCode(exitcode.Partial, err)
	if st := f.lastStatus(); st.State != state.StatePartial || st.ExitCode != exitcode.Partial {
		t.Fatalf("status %+v", st)
	}
	parts := f.partials()
	if len(parts) != 1 || len(f.committed()) != 0 {
		t.Fatalf("partials=%v committed=%d", parts, len(f.committed()))
	}
	rep, jerr := journal.Read(filepath.Join(parts[0], journal.Name))
	if jerr != nil || len(rep.Entries) == 0 || len(rep.Entries) >= 6 {
		t.Fatalf("journal %+v %v", rep, jerr)
	}

	f.e.AfterCopy = nil
	f.step = time.Hour // a later clock must not change the resumed backup's name
	f.mustRun()
	if got := f.latest(); got != strings.TrimSuffix(parts[0], backupset.PartialSuffix) {
		t.Fatalf("resumed into %s, want the original name", got)
	}
	if !strings.Contains(f.out.String(), "resume: kept") {
		t.Errorf("resume did not report kept files:\n%s", f.out.String())
	}
	if d := diffSnaps(want, snap(t, f.latest(), backupMeta...)); d != "" {
		t.Fatalf("resumed result differs from a clean run:\n%s", d)
	}
	f.sameAsSource(f.latest())
	if len(f.partials()) != 0 {
		t.Error(".partial remains after the resumed run")
	}
}

func TestResumeAfterAnUncleanStopReverifiesJournaledFiles(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(4)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	part := f.partials()[0]
	rep, _ := journal.Read(filepath.Join(part, journal.Name))
	var victim string
	for p := range rep.Entries {
		victim = p
	}
	forgetCleanStop(t, part)                                     // as if the run had been killed instead of stopped
	flipByte(t, filepath.Join(part, filepath.FromSlash(victim))) // corrupt a journaled file
	f.e.AfterCopy = nil
	f.mustRun()
	if !strings.Contains(f.out.String(), "resume: "+victim+" did not verify") {
		t.Errorf("tampering with %s not noticed:\n%s", victim, f.out.String())
	}
	f.sameAsSource(f.latest())
}

func TestResumeOverwritesUnjournaledHalfWrittenFile(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(1)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	part := f.partials()[0]
	rep, _ := journal.Read(filepath.Join(part, journal.Name))
	var open string
	for _, rel := range []string{"sub/b.bin", "a.txt", "sub/deep/c.txt"} {
		if _, done := rep.Entries[rel]; !done {
			open = rel
			break
		}
	}
	junk := filepath.Join(part, filepath.FromSlash(open))
	if err := os.WriteFile(junk, []byte("half written garbage that is longer than nothing"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.e.AfterCopy = nil
	f.mustRun()
	f.sameAsSource(f.latest())
}

func TestResumeWhenSourceChangedSinceInterruption(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(3)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	f.put("a.txt", []byte("changed after the interruption, different size"))
	f.put("a.txt", []byte("changed after the interruption, different size"))
	later := baseTime.Add(72 * time.Hour)
	_ = os.Chtimes(f.path("a.txt"), later, later)
	f.e.AfterCopy = nil
	f.mustRun()
	f.sameAsSource(f.latest())
}

func TestDeadlineStopsCleanlyAndKeepsEverything(t *testing.T) {
	f := newFixture(t)
	f.j.Deadline = "250ms"
	f.j.OnFailure = `echo "$BACKUP_CLI_STATUS:$BACKUP_CLI_EXIT_CODE" > ` + filepath.Join(f.root, "hook.out")
	f.save()
	sentinel := filepath.Join(f.dst, "unrelated.txt")
	if err := os.WriteFile(sentinel, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	f.e.AfterCopy = func(string) {
		if calls.Add(1) == 1 {
			time.Sleep(400 * time.Millisecond) // run past the deadline
		}
	}
	srcBefore := snap(t, f.src)
	err := f.run(func(r *cli.EngineRequest) { r.Jobs = 1 })
	f.expectCode(exitcode.Partial, err)
	if !strings.Contains(err.Error(), "deadline reached") {
		t.Errorf("message: %v", err)
	}
	st := f.lastStatus()
	if st.State != state.StatePartial || st.ExitCode != 3 {
		t.Fatalf("%+v", st)
	}
	parts := f.partials()
	if len(parts) != 1 || len(f.committed()) != 0 {
		t.Fatalf("partials %v committed %d", parts, len(f.committed()))
	}
	if _, err := os.Stat(filepath.Join(parts[0], journal.Name)); err != nil {
		t.Error("journal missing after deadline")
	}
	if b, _ := os.ReadFile(sentinel); string(b) != "keep me" {
		t.Error("an unrelated file in the destination was touched")
	}
	if d := diffSnaps(srcBefore, snap(t, f.src)); d != "" {
		t.Fatalf("source changed:\n%s", d)
	}
	if b, _ := os.ReadFile(filepath.Join(f.root, "hook.out")); strings.TrimSpace(string(b)) != "partial:3" {
		t.Errorf("a partial run must fire the on-failure hook: %q", b)
	}
	resumeAfterDeadline(t, f, parts[0])
}

func resumeAfterDeadline(t *testing.T, f *fixture, part string) {
	t.Helper()
	before := snap(t, part)
	f.j.Deadline = ""
	f.save()
	f.e.AfterCopy = nil
	f.mustRun()
	for rel, n := range before {
		if rel == journal.Name || n.Typ != "file" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(f.latest(), rel)); err != nil {
			t.Errorf("%s from the partial run vanished: %v", rel, err)
		}
	}
	f.sameAsSource(f.latest())
}

func TestAlreadyExpiredDeadlineIsPartial(t *testing.T) {
	f := newFixture(t)
	f.j.Deadline = "1ns"
	f.save()
	f.expectCode(exitcode.Partial, f.run())
	if st := f.lastStatus(); st.State != state.StatePartial {
		t.Fatalf("%+v", st)
	}
}

func TestBadDeadlineIsAUsageError(t *testing.T) {
	f := newFixture(t)
	f.j.Deadline = "tomorrow-ish"
	f.expectCode(exitcode.Usage, f.run())
	if runs, _ := f.store.ListRuns(""); len(runs) != 0 {
		t.Error("a usage error must not create a run")
	}
}

func TestFileChangingMidCopyIsRetriedOnce(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int64
	f.e.AfterCopy = func(rel string) {
		if rel == "a.txt" && calls.Add(1) == 1 {
			later := baseTime.Add(100 * time.Hour)
			_ = os.Chtimes(f.path("a.txt"), later, later)
		}
	}
	f.mustRun()
	st := f.lastStatus()
	if calls.Load() != 2 || hasWarning(st, "unstable") || st.State != state.StateSucceeded {
		t.Fatalf("calls=%d status=%+v", calls.Load(), st)
	}
	if !strings.Contains(f.out.String(), "a.txt changed while it was being copied; retrying") {
		t.Error("retry not logged")
	}
	f.sameAsSource(f.latest())
}

func TestFileThatKeepsChangingIsWarnedAboutButRunSucceeds(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int64
	f.e.AfterCopy = func(rel string) {
		if rel == "a.txt" {
			n := calls.Add(1)
			ts := baseTime.Add(time.Duration(100+n) * time.Hour)
			_ = os.Chtimes(f.path("a.txt"), ts, ts)
		}
	}
	f.mustRun()
	st := f.lastStatus()
	if calls.Load() != 2 || !hasWarning(st, "unstable: a.txt") || st.State != state.StateSucceeded || st.ExitCode != 0 {
		t.Fatalf("calls=%d status=%+v", calls.Load(), st)
	}
}

func TestGrowingFileIsDetected(t *testing.T) {
	f := newFixture(t)
	f.e.AfterCopy = func(rel string) {
		if rel == "a.txt" {
			fh, _ := os.OpenFile(f.path("a.txt"), os.O_APPEND|os.O_WRONLY, 0)
			_, _ = fh.WriteString("+")
			_ = fh.Close()
		}
	}
	f.mustRun()
	if st := f.lastStatus(); !hasWarning(st, "unstable: a.txt") {
		t.Fatalf("%+v", st)
	}
}

// forgetCleanStop rewrites the partial's journal without its clean-stop
// records, which is what a SIGKILL or a power loss leaves behind.
func forgetCleanStop(t *testing.T, part string) {
	t.Helper()
	path := filepath.Join(part, journal.Name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.Contains(l, `"t":"s"`) {
			keep = append(keep, l)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(keep, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// After a clean stop (deadline or signal) the journaled files were fsynced by
// this tool, so a resume trusts them by size and mtime. Corrupting one without
// changing either proves that no hash was computed.
func TestResumeAfterACleanStopTrustsJournaledFilesWithoutHashing(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(4)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	part := f.partials()[0]
	rep, _ := journal.Read(filepath.Join(part, journal.Name))
	var victim string
	for p, e := range rep.Entries {
		if e.Size > 0 && rep.Trusted(p) {
			victim = p
		}
	}
	if victim == "" {
		t.Fatal("the clean stop was not recorded")
	}
	flipByte(t, filepath.Join(part, filepath.FromSlash(victim)))
	f.e.AfterCopy = nil
	f.mustRun(func(r *cli.EngineRequest) { r.VerifySample = 0 }) // the sample verify could pick the tampered file
	if strings.Contains(f.out.String(), "did not verify") {
		t.Errorf("a trusted file was re-hashed:\n%s", f.out.String())
	}
	if !strings.Contains(f.out.String(), "resume: kept 4 journaled") {
		t.Errorf("the journaled files were not kept:\n%s", f.out.String())
	}
}

func TestResumeAfterACleanStopStillChecksSizeAndMtime(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	ctx := f.interruptAfter(4)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	part := f.partials()[0]
	rep, _ := journal.Read(filepath.Join(part, journal.Name))
	var victim string
	for p := range rep.Entries {
		victim = p
	}
	if err := os.WriteFile(filepath.Join(part, filepath.FromSlash(victim)), []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.e.AfterCopy = nil
	f.mustRun()
	f.sameAsSource(f.latest())
}

// Repeated deadline runs must make progress: a resume keeps the files of a
// clean stop, so the second window is spent on new data, not on hashing.
func TestDeadlineRunsConvergeWithoutRehashing(t *testing.T) {
	f := newFixture(t)
	f.step = 0
	for _, stopAfter := range []int64{2, 2} {
		ctx := f.interruptAfter(stopAfter)
		f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	}
	f.e.AfterCopy = nil
	f.out.Reset()
	f.mustRun()
	if !strings.Contains(f.out.String(), "resume: kept 4 journaled") {
		t.Errorf("earlier windows' files were not kept:\n%s", f.out.String())
	}
	f.sameAsSource(f.latest())
}
