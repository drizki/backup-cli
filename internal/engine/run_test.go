package engine

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/state"
)

func TestFullRunLayoutAndManifests(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	dir := f.latest()
	f.sameAsSource(dir)

	if want := f.j.DstFor(baseTime.Add(f.step)); dir != want {
		t.Errorf("final dir %s, want %s", dir, want)
	}
	if len(f.partials()) != 0 {
		t.Error(".partial left after a successful run")
	}
	if _, err := os.Lstat(filepath.Join(dir, journal.Name)); !isNotExist(err) {
		t.Error("journal left in the committed backup")
	}
	checkMarkerAndManifest(t, dir)
	checkStatusOfFullRun(t, f, dir)
}

func checkMarkerAndManifest(t *testing.T, dir string) {
	t.Helper()
	m, err := backupset.ReadMarker(dir)
	if err != nil || m.Job != "docs" || m.FileCount != 6 || m.RunID == "" {
		t.Fatalf("marker %+v %v", m, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, manifest.TSVName))
	entries, err := manifest.ParseTSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Path != "." || len(entries) != 1+6+4+2 { // root, files, dirs, links
		t.Errorf("%d manifest entries", len(entries))
	}
	rep, err := manifest.Verify(context.Background(), dir, manifest.VerifyOptions{SamplePct: 100})
	if err != nil || !rep.OK() || len(rep.Warnings) != 0 {
		t.Fatalf("verify: %+v %v", rep, err)
	}
}

func checkStatusOfFullRun(t *testing.T, f *fixture, dir string) {
	t.Helper()
	st := f.lastStatus()
	if st.State != state.StateSucceeded || st.ExitCode != 0 || st.FilesTotal != 6 || st.FilesCopiedFromSource != 6 ||
		st.FinalDir != dir || st.Log == "" || st.PID != os.Getpid() || !st.Terminal() || st.Finished.IsZero() {
		t.Fatalf("status %+v", st)
	}
	if st.Warnings == nil || st.Errors == nil {
		t.Error("warnings and errors must be empty lists, not null")
	}
	if b, _ := os.ReadFile(st.Log); !strings.Contains(string(b), "committed "+dir) {
		t.Errorf("run.log: %s", b)
	}
}

func TestSha256sumAcceptsManifest(t *testing.T) {
	bin, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("sha256sum binary missing")
	}
	f := newFixture(t)
	f.mustRun()
	cmd := exec.Command(bin, "-c", "--quiet", manifest.SHA256Name)
	cmd.Dir = f.latest()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sha256sum -c: %v\n%s", err, out)
	}
}

func TestSourceIsNeverModified(t *testing.T) {
	for _, jobs := range []int{1, 4} {
		f := newFixture(t)
		f.mustRun(func(r *cli.EngineRequest) { r.Jobs = jobs })
		f.put("extra.txt", []byte("added by the test, then backed up"))
		before := snap(t, f.src)
		f.mustRun(func(r *cli.EngineRequest) { r.Jobs = jobs })
		if d := diffSnaps(before, snap(t, f.src)); d != "" {
			t.Fatalf("jobs=%d: source changed:\n%s", jobs, d)
		}
		f.sameAsSource(f.latest())
	}
}

func TestWorkerCountsProduceIdenticalBackups(t *testing.T) {
	var snaps []map[string]node
	var manifests []string
	for _, jobs := range []int{1, 4} {
		f := newFixture(t)
		f.mustRun(func(r *cli.EngineRequest) { r.Jobs = jobs })
		snaps = append(snaps, snap(t, f.latest(), backupMeta...))
		tsv, _ := os.ReadFile(filepath.Join(f.latest(), manifest.TSVName))
		sum, _ := os.ReadFile(filepath.Join(f.latest(), manifest.SHA256Name))
		manifests = append(manifests, string(tsv)+string(sum))
	}
	if d := diffSnaps(snaps[0], snaps[1]); d != "" {
		t.Fatalf("jobs=1 and jobs=4 differ:\n%s", d)
	}
	if manifests[0] != manifests[1] {
		t.Fatal("manifests differ between jobs=1 and jobs=4")
	}
}

func TestSymlinksSpecialsAndNames(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.link("abs", "/etc/hostname")
	f.link("out", outside)
	f.link("up", "../outside")
	if err := syscall.Mkfifo(f.path("pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	dir := f.latest()
	for name, target := range map[string]string{"link-rel": "a.txt", "dangling": "no/such/target", "abs": "/etc/hostname", "out": outside, "up": "../outside"} {
		got, err := os.Readlink(filepath.Join(dir, name))
		if err != nil || got != target {
			t.Errorf("symlink %s -> %q (%v), want %q", name, got, err, target)
		}
	}
	if _, followed := snap(t, dir)["out/secret"]; followed {
		t.Error("a symlinked directory was followed into the backup")
	}
	if _, err := os.Lstat(filepath.Join(dir, "pipe")); !isNotExist(err) {
		t.Error("fifo was backed up")
	}
	if fi, err := os.Stat(filepath.Join(dir, "empty")); err != nil || !fi.IsDir() {
		t.Error("empty directory not preserved")
	}
	st := f.lastStatus()
	if !hasWarning(st, "1 special file(s) skipped") || !strings.Contains(f.out.String(), "skipped pipe (fifo)") {
		t.Errorf("skipped special file not reported: %v", st.Warnings)
	}
	b, err := os.ReadFile(filepath.Join(dir, "new\nline.txt"))
	if err != nil || string(b) != "newline name" {
		t.Errorf("newline name: %q %v", b, err)
	}
}

func TestSecondRunReadsOnlyChangedFilesFromSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	first := f.latest()

	f.put("a.txt", []byte("alpha changed")) // changed
	f.put("fresh.txt", []byte("brand new")) // new
	if err := os.Remove(f.path("sub/deep/c.txt")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.txt", "fresh.txt"} {
		ts := baseTime.Add(48 * time.Hour)
		if err := os.Chtimes(f.path(rel), ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	srcReads, prevReads := map[string]int{}, map[string]int{}
	prevRoot, _ := os.OpenRoot(first)
	t.Cleanup(func() { _ = prevRoot.Close() })
	srcRoot, _ := os.OpenRoot(f.src)
	t.Cleanup(func() { _ = srcRoot.Close() })
	f.e.OpenSource = func(rel string) (*os.File, error) {
		mu.Lock()
		srcReads[rel]++
		mu.Unlock()
		return srcRoot.Open(rel)
	}
	f.e.OpenPrev = func(rel string) (*os.File, error) {
		mu.Lock()
		prevReads[rel]++
		mu.Unlock()
		return prevRoot.Open(rel)
	}
	f.mustRun()
	f.sameAsSource(f.latest())
	if len(srcReads) != 2 || srcReads["a.txt"] != 1 || srcReads["fresh.txt"] != 1 {
		t.Errorf("only changed and new files may be read from the source: %v", srcReads)
	}
	if len(prevReads) != 4 || prevReads["sub/b.bin"] != 1 {
		t.Errorf("unchanged files must come from the previous backup: %v", prevReads)
	}
	if _, err := os.Lstat(filepath.Join(f.latest(), "sub", "deep", "c.txt")); !isNotExist(err) {
		t.Error("a file removed from the source is still in the new backup")
	}
	if _, err := os.Lstat(filepath.Join(first, "sub", "deep", "c.txt")); err != nil {
		t.Error("the previous backup lost a file")
	}
}

func TestUnchangedFilesAreNotReadFromSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	first := f.latest()
	var mu sync.Mutex
	srcReads := map[string]int{}
	prevRoot, _ := os.OpenRoot(first)
	t.Cleanup(func() { _ = prevRoot.Close() })
	srcRoot, _ := os.OpenRoot(f.src)
	t.Cleanup(func() { _ = srcRoot.Close() })
	f.e.OpenSource = func(rel string) (*os.File, error) {
		mu.Lock()
		srcReads[rel]++
		mu.Unlock()
		return srcRoot.Open(rel)
	}
	f.e.OpenPrev = prevRoot.Open
	f.mustRun()
	if len(srcReads) != 0 {
		t.Fatalf("unchanged files were read from the source: %v", srcReads)
	}
	st := f.lastStatus()
	if st.FilesCopiedFromPrev != 6 || st.FilesCopiedFromSource != 0 || st.BytesFromSource != 0 {
		t.Fatalf("%+v", st)
	}
	f.sameAsSource(f.latest())
}

func TestFromSourceIgnoresPreviousBackup(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	prevCalls := 0
	f.e.OpenPrev = func(string) (*os.File, error) { prevCalls++; return nil, os.ErrNotExist }
	f.mustRun(func(r *cli.EngineRequest) { r.FromSource = true })
	if prevCalls != 0 {
		t.Fatalf("previous backup read %d times with --from-source", prevCalls)
	}
	if st := f.lastStatus(); st.FilesCopiedFromSource != 6 || st.FilesCopiedFromPrev != 0 {
		t.Fatalf("%+v", st)
	}
	f.sameAsSource(f.latest())
}

func TestBitrotInPreviousBackupIsRecopiedFromSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	first := f.latest()
	flipByte(t, filepath.Join(first, "sub", "b.bin")) // same size, same mtime
	f.mustRun()
	st := f.lastStatus()
	if st.State != state.StateSucceeded || !hasWarning(st, "BITROT") || !strings.Contains(f.out.String(), "sub/b.bin") {
		t.Fatalf("bitrot not reported: %+v", st)
	}
	if st.FilesCopiedFromSource != 1 || st.FilesCopiedFromPrev != 5 {
		t.Errorf("counts %+v", st)
	}
	f.sameAsSource(f.latest())
}

func TestCorruptPreviousManifestFallsBackToSource(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	tsv := filepath.Join(f.latest(), manifest.TSVName)
	b, _ := os.ReadFile(tsv)
	if err := os.WriteFile(tsv, append(b, "x\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	st := f.lastStatus()
	if !hasWarning(st, "does not match its marker") || st.FilesCopiedFromSource != 6 {
		t.Fatalf("%+v", st)
	}
	f.sameAsSource(f.latest())
}

func TestUnreadableSourceFileFailsAndKeepsPartial(t *testing.T) {
	f := newFixture(t)
	srcRoot, _ := os.OpenRoot(f.src)
	t.Cleanup(func() { _ = srcRoot.Close() })
	f.e.OpenSource = func(rel string) (*os.File, error) {
		if rel == "a.txt" {
			return nil, syscall.EACCES
		}
		return srcRoot.Open(rel)
	}
	err := f.run()
	f.expectCode(exitcode.Failed, err)
	if st := f.lastStatus(); st.State != state.StateFailed || !strings.Contains(strings.Join(st.Errors, " "), "a.txt") {
		t.Fatalf("%+v", st)
	}
	if len(f.partials()) != 1 || len(f.committed()) != 0 {
		t.Fatal("a failed run must keep its .partial and commit nothing")
	}
	f.e.OpenSource = nil
	f.mustRun() // resumes and completes
	f.sameAsSource(f.latest())
}

func TestBiweeklyGateIgnoresAMarkerDatedInTheFuture(t *testing.T) {
	f := newFixture(t)
	f.j.Biweekly = true
	f.save()
	f.now = baseTime.AddDate(3, 0, 0) // clock wrong: three years ahead
	f.mustRun()
	f.now = baseTime.AddDate(0, 1, 0) // clock fixed
	f.step = 0
	f.mustRun()
	runs, _ := f.store.ListRuns(f.j.Name)
	warned := false
	for _, st := range runs {
		if st.State == state.StateSkipped {
			t.Errorf("run %s skipped by the biweekly gate: %s", st.RunID, st.SkipReason)
		}
		warned = warned || hasWarning(st, "clock skew")
	}
	if !warned {
		t.Errorf("no clock-skew warning in the runs: %+v", runs)
	}
}
