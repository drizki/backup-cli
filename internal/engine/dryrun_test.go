package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/lock"
	"github.com/drizki/backup-cli/internal/state"
)

// trees snapshots the three places a dry run must not touch.
func (f *fixture) trees() [3]map[string]node {
	return [3]map[string]node{snap(f.t, f.src), snap(f.t, f.dst), snap(f.t, f.store.Dir)}
}

func (f *fixture) expectUntouched(before [3]map[string]node) {
	f.t.Helper()
	for i, name := range []string{"source", "destination", "state dir"} {
		if d := diffSnaps(before[i], f.trees()[i]); d != "" {
			f.t.Fatalf("dry run changed the %s:\n%s", name, d)
		}
	}
}

func TestDryRunWritesNothingAnywhere(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	f.put("a.txt", []byte("changed since the first backup"))
	later := baseTime.Add(50 * time.Hour)
	_ = os.Chtimes(f.path("a.txt"), later, later)
	f.out.Reset()
	before := f.trees()
	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	f.expectUntouched(before)
	out := f.out.String()
	for _, want := range []string{"dry run for job docs: nothing was written", "copy from source:  1 file(s)",
		"copy from previous backup: 5 file(s)", "prune plan:", "previous backup:   " + f.latest()} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestDryRunOnAFreshStateDirCreatesNoDirectories(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.store.Dir); err != nil {
		t.Fatal(err)
	}
	beforeDst := snap(t, f.dst)
	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	if _, err := os.Lstat(f.store.Dir); !isNotExist(err) {
		t.Fatal("dry run created the state directory")
	}
	if d := diffSnaps(beforeDst, snap(t, f.dst)); d != "" {
		t.Fatal(d)
	}
}

func TestDryRunHonoursGuardsAndLock(t *testing.T) {
	f := newFixture(t)
	if err := f.store.Ensure(); err != nil {
		t.Fatal(err)
	}
	held, _ := lock.Acquire(f.store.LockPath("docs"))
	before := f.trees()
	err := f.run(func(r *cli.EngineRequest) { r.DryRun = true })
	f.expectCode(exitcode.Refused, err)
	_ = held.Close()
	f.expectUntouched(before)

	f.e.Statfs = func(string) (uint64, error) { return 10, nil }
	f.expectCode(exitcode.Refused, f.run(func(r *cli.EngineRequest) { r.DryRun = true }))
}

func TestDryRunShowsPrunePlanAndResume(t *testing.T) {
	f := newFixture(t)
	f.j.Keep = 2
	f.save()
	f.mustRun()
	f.mustRun()
	f.out.Reset()
	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	if !strings.Contains(f.out.String(), "would remove 1 old backup(s)") {
		t.Errorf("prune plan missing:\n%s", f.out.String())
	}
	for _, rel := range []string{"a.txt", "sub/b.bin"} { // changed files, so there is something to interrupt
		f.put(rel, []byte("changed "+rel))
		ts := baseTime.Add(90 * time.Hour)
		_ = os.Chtimes(f.path(rel), ts, ts)
	}
	f.step = 0
	ctx := f.interruptAfter(1)
	f.expectCode(exitcode.Partial, f.runCtx(ctx, func(r *cli.EngineRequest) { r.Jobs = 1 }))
	f.out.Reset()
	before := f.trees()
	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	f.expectUntouched(before)
	if !strings.Contains(f.out.String(), "resumes partial:") {
		t.Errorf("resume not shown:\n%s", f.out.String())
	}
}

func TestDryRunWithoutOutputWriter(t *testing.T) {
	f := newFixture(t)
	req := f.request()
	req.DryRun, req.Stdout = true, nil
	if err := f.e.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestPruneNames(t *testing.T) {
	if got := pruneNames(nil, 3); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestHooksReceiveEnvironment(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(f.root, "hook.out")
	f.j.OnSuccess = `printf '%s|%s|%s|%s|%s|%s' "$BACKUP_CLI_JOB" "$BACKUP_CLI_RUN_ID" "$BACKUP_CLI_STATUS" "$BACKUP_CLI_LOG" "$BACKUP_CLI_BACKUP_PATH" "$BACKUP_CLI_EXIT_CODE" > ` + out
	f.j.OnFailure = "echo wrong hook > " + filepath.Join(f.root, "wrong.out")
	f.save()
	f.mustRun()
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	st := f.lastStatus()
	want := strings.Join([]string{"docs", st.RunID, "succeeded", st.Log, f.latest(), "0"}, "|")
	if string(b) != want {
		t.Fatalf("hook env %q, want %q", b, want)
	}
	if _, err := os.Stat(filepath.Join(f.root, "wrong.out")); err == nil {
		t.Error("the failure hook ran for a successful run")
	}
}

func TestFailingAndSlowHooksNeverChangeTheResult(t *testing.T) {
	f := newFixture(t)
	f.j.OnSuccess = "echo going to fail >&2; exit 9"
	f.save()
	f.mustRun()
	if st := f.lastStatus(); st.State != state.StateSucceeded || st.ExitCode != 0 {
		t.Fatalf("%+v", st)
	}
	if !strings.Contains(f.out.String(), "hook failed") || !strings.Contains(f.out.String(), "going to fail") {
		t.Errorf("hook failure not logged:\n%s", f.out.String())
	}

	f.j.OnSuccess = "sleep 20"
	f.save()
	f.e.HookTimeout = 200 * time.Millisecond
	start := time.Now()
	f.mustRun()
	if time.Since(start) > 10*time.Second || !strings.Contains(f.out.String(), "timed out") {
		t.Fatalf("hook timeout not enforced (%v):\n%s", time.Since(start), f.out.String())
	}
}

func TestFailureHookFiresForRefusedRuns(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(f.root, "hook.out")
	f.j.OnFailure = `printf '%s:%s' "$BACKUP_CLI_STATUS" "$BACKUP_CLI_EXIT_CODE" > ` + out
	f.save()
	f.e.Statfs = func(string) (uint64, error) { return 1, nil }
	f.expectCode(exitcode.Refused, f.run())
	if b, _ := os.ReadFile(out); string(b) != "refused:4" {
		t.Fatalf("%q", b)
	}
}

func TestSkippedAndDryRunsFireNoHooks(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.root, "fired")
	f.j.OnSuccess, f.j.OnFailure = "touch "+marker, "touch "+marker
	f.j.Biweekly = true
	f.save()
	f.mustRun()
	_ = os.Remove(marker)
	f.step = time.Hour
	f.mustRun() // skipped
	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook fired for a skipped or dry run")
	}
}

func TestStatusIsRunningWithProgressWhileCopying(t *testing.T) {
	f := newFixture(t)
	f.e.ProgressEvery = 10 * time.Millisecond
	var mid state.Status
	var logPath string
	f.e.AfterCopy = func(rel string) {
		if rel != "a.txt" {
			return
		}
		time.Sleep(80 * time.Millisecond)
		runs, _ := f.store.ListRuns("docs")
		if len(runs) == 1 {
			mid = runs[0]
			logPath = runs[0].Log
		}
	}
	f.mustRun(func(r *cli.EngineRequest) { r.Jobs = 1 })
	if mid.State != state.StateRunning || mid.Terminal() || mid.PID != os.Getpid() || mid.FilesTotal != 6 || mid.PartialDir == "" {
		t.Fatalf("mid-run status %+v", mid)
	}
	if b, _ := os.ReadFile(logPath); !strings.Contains(string(b), "run ") {
		t.Error("log empty while running")
	}
}

func TestStatusJSONHasEveryField(t *testing.T) {
	f := newFixture(t)
	f.mustRun()
	b, err := os.ReadFile(f.store.StatusPath(f.lastStatus().RunID))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"run_id", "job", "state", "started", "finished", "pid", "tool_version", "source", "dest", "partial_dir",
		"final_dir", "files_total", "files_copied_from_source", "files_copied_from_prev", "bytes_total", "bytes_from_source",
		"warnings", "errors", "exit_code", "refuse_reason", "dry_run", "log"} {
		if !strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("status.json lacks %q", key)
		}
	}
}

func TestEngineNeverDeletes(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(p)
		for _, bad := range []string{"os.Remove", "RemoveAll", "Unlink", "unix.Unlinkat"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s contains %q: the engine must never delete", p, bad)
			}
		}
	}
}

func TestPackageRunUsesProductionDefaults(t *testing.T) {
	f := newFixture(t)
	req := f.request()
	if err := Run(context.Background(), req); err != nil {
		t.Fatalf("%v\n%s", err, f.out.String())
	}
	if len(f.committed()) != 1 {
		t.Fatal("no backup")
	}
}
