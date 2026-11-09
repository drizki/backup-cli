package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/state"
)

func TestHealthyEnvironmentHasNoWarningsOrFailures(t *testing.T) {
	f := newFixture(t)
	f.backup(1, 1<<20)
	f.status("20260331T120000Z-docs-aaaa", state.StateSucceeded, now.Add(-time.Hour), nil)
	r := f.run()
	if r.Failed() || r.Warnings != 0 || !r.OK {
		for _, c := range r.Checks {
			t.Logf("%s %s %s: %s", c.Severity, c.Name, c.Job, c.Detail)
		}
		t.Fatalf("failures=%d warnings=%d", r.Failures, r.Warnings)
	}
	expect(t, r, "state directory", "", OK, "writable")
	expect(t, r, "systemd", "", OK, "available")
	expect(t, r, "version", "", OK, "v9.9.9-test")
	expect(t, r, "source readable", "docs", OK, f.src)
	expect(t, r, "source writable", "docs", OK, "read-only")
	expect(t, r, "destination writable", "docs", OK, f.dst)
	expect(t, r, "destination mount", "docs", OK, "xfs")
	expect(t, r, "backups", "docs", OK, "1 committed")
	expect(t, r, "free space", "docs", OK, "fit")
	expect(t, r, "leftover partial", "docs", OK, "no leftover")
	expect(t, r, "last run", "docs", OK, "succeeded")
	expect(t, r, "lock", "docs", OK, "no lock file")
	expect(t, r, "timer", "docs", OK, "active")
}

func TestNoJobs(t *testing.T) {
	f := newFixture(t)
	if err := f.env.Store.RemoveJob("docs"); err != nil {
		t.Fatal(err)
	}
	expect(t, f.run(), "jobs", "", OK, "no jobs")
}

func TestCorruptJobFileFails(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.env.Store.Dir, "jobs", "bad.json"), "{")
	r := f.run()
	if !r.Failed() {
		t.Fatal("a corrupt job file must fail")
	}
	expect(t, r, "job file", "", Fail, "bad")
}

func TestZFSBlockCloningParameter(t *testing.T) {
	f := newFixture(t)
	expect(t, f.run(), "zfs block cloning", "", OK, "not applicable")
	param := filepath.Join(f.sys, "module/zfs/parameters/zfs_bclone_enabled")
	for content, want := range map[string]struct {
		sev    Severity
		detail string
	}{
		"0\n":     {OK, "physically separate"},
		"1\n":     {Warn, "may share blocks"},
		"maybe\n": {Warn, "unexpected value"},
	} {
		write(t, param, content)
		r := f.run()
		expect(t, r, "zfs block cloning", "", want.sev, want.detail)
		if r.Failed() {
			t.Fatal("a ZFS warning must not fail doctor")
		}
	}
}

func TestStateDirectory(t *testing.T) {
	f := newFixture(t)
	dir := f.env.Store.Dir

	delete(f.writable, dir)
	expect(t, f.run(), "state directory", "", Fail, "not writable")

	f.writable[dir] = true
	f.env.Store.Dir = filepath.Join(f.root, "missing-state")
	r := f.run()
	expect(t, r, "state directory", "", Warn, "does not exist yet")
	if r.Failed() {
		t.Fatal("a missing state dir is a warning")
	}

	file := filepath.Join(f.root, "afile")
	write(t, file, "x")
	f.env.Store.Dir = file
	expect(t, f.run(), "state directory", "", Fail, "not a directory")

	f.env.Store.Dir = filepath.Join(file, "below")
	expect(t, f.run(), "state directory", "", Fail, "below")
}

func TestSystemd(t *testing.T) {
	f := newFixture(t)
	f.fake.Avail = false
	r := f.run()
	expect(t, r, "systemd", "", Warn, "not available")
	expect(t, r, "timer", "docs", Warn, "systemd is not available")
	if r.Failed() {
		t.Fatal("missing systemd is a warning")
	}

	f.env.Manager = nil
	expect(t, f.run(), "systemd", "", Warn, "not available")

	f.env.Manager = f.fake
	f.fake.Avail = true
	delete(f.fake.Enabled, "backup-cli-docs")
	expect(t, f.run(), "timer", "docs", Warn, "not active")

	f.fake.FailOn["IsActive"] = errors.New("bus down")
	expect(t, f.run(), "timer", "docs", Warn, "bus down")

	f.fake.FailOn = map[string]error{}
	f.j.Scheduled = false
	f.save()
	expect(t, f.run(), "timer", "docs", OK, "not scheduled")
}

func TestSource(t *testing.T) {
	f := newFixture(t)
	f.writable[f.src] = true
	r := f.run()
	expect(t, r, "source writable", "docs", Warn, "can modify")
	if r.Failed() {
		t.Fatal("a writable source is only a warning")
	}

	f.denyRead[f.src] = true
	expect(t, f.run(), "source readable", "docs", Fail, "not readable")

	f.denyRead = map[string]bool{}
	file := filepath.Join(f.root, "file-source")
	write(t, file, "x")
	f.j.Source = file
	f.save()
	expect(t, f.run(), "source readable", "docs", Fail, "not a directory")

	f.j.Source = filepath.Join(f.root, "gone")
	f.save()
	expect(t, f.run(), "source readable", "docs", Fail, "gone")
}

func TestOverlapIsAFailure(t *testing.T) {
	f := newFixture(t)
	f.j.DstTemplate = filepath.Join(f.src, "docs-{timestamp}")
	f.save()
	f.writable[f.src] = true
	expect(t, f.run(), "overlap", "docs", Fail, "inside the source")
}

func TestDestination(t *testing.T) {
	f := newFixture(t)
	delete(f.writable, f.dst)
	expect(t, f.run(), "destination writable", "docs", Fail, "not writable")

	f.j.DstTemplate = filepath.Join(f.root, "no-such", "docs-{timestamp}")
	f.save()
	expect(t, f.run(), "destination writable", "docs", Fail, "no-such")

	file := filepath.Join(f.root, "dst-file")
	write(t, file, "x")
	f.j.DstTemplate = filepath.Join(file, "docs-{timestamp}")
	f.save()
	expect(t, f.run(), "destination writable", "docs", Fail, "not a directory")
}

func TestDestinationMount(t *testing.T) {
	f := newFixture(t)
	f.mounts("22 1 8:1 / / rw - ext4 /dev/sda1 rw\n")
	r := f.run()
	expect(t, r, "destination mount", "docs", Warn, "root filesystem")
	if r.Failed() {
		t.Fatal("warning only")
	}
	f.mounts("short\n")
	expect(t, f.run(), "destination mount", "docs", Warn, "not covered")
	f.env.MountInfo = filepath.Join(f.root, "nope")
	expect(t, f.run(), "destination mount", "docs", Warn, "cannot read the mount table")
}

func TestFreeSpace(t *testing.T) {
	f := newFixture(t)
	expect(t, f.run(), "free space", "docs", OK, "no backup yet")

	f.backup(1, 10<<30)
	f.free = 1 << 30
	r := f.run()
	expect(t, r, "free space", "docs", Fail, "keep 3 + 1")
	if !r.Failed() {
		t.Fatal("low space must fail doctor")
	}

	f.free = 100 << 30
	expect(t, f.run(), "free space", "docs", OK, "fit")

	f.env.Statfs = func(string) (uint64, error) { return 0, errors.New("statfs broke") }
	expect(t, f.run(), "free space", "docs", Warn, "statfs broke")
}

func TestFreeSpaceCountsExistingBackups(t *testing.T) {
	f := newFixture(t)
	for n := 1; n <= 3; n++ {
		f.backup(n, 10<<30)
	}
	f.free = 11 << 30 // keep=3 and 3 exist: one more backup is needed
	expect(t, f.run(), "free space", "docs", OK, "fit")
	f.free = 9 << 30
	expect(t, f.run(), "free space", "docs", Fail, "1 more backup")
}

func TestLeftoverPartialAndUnknownDirectories(t *testing.T) {
	f := newFixture(t)
	f.backup(1, 1)
	mkdir(t, filepath.Join(f.dst, "docs-20260401T000000Z.partial"))
	mkdir(t, filepath.Join(f.dst, "docs-20260402T000000Z")) // no marker
	write(t, filepath.Join(f.dst, "docs-20260403T000000Z", ".backup-cli.json"), "{")
	mkdir(t, filepath.Join(f.dst, "unrelated"))
	r := f.run()
	expect(t, r, "leftover partial", "docs", Warn, "docs-20260401T000000Z.partial")
	expect(t, r, "unrecognized directories", "docs", Warn, "docs-20260402T000000Z (no-marker)")
	expect(t, r, "unrecognized directories", "docs", Warn, "docs-20260403T000000Z (bad-marker)")
	if strings.Contains(find(t, r, "unrecognized directories", "docs").Detail, "unrelated") {
		t.Fatal("directories outside the template are not reported")
	}
	if r.Failed() {
		t.Fatal("leftovers are warnings")
	}
	if _, err := os.Stat(filepath.Join(f.dst, "docs-20260401T000000Z.partial")); err != nil {
		t.Fatal("doctor must not delete anything")
	}
}

func TestLastRun(t *testing.T) {
	f := newFixture(t)
	expect(t, f.run(), "last run", "docs", Warn, "never run")
	f.j.Scheduled = false
	f.save()
	expect(t, f.run(), "last run", "docs", OK, "never run")

	cases := []struct {
		name  string
		state string
		mod   func(*state.Status)
		sev   Severity
		text  string
	}{
		{"failed", state.StateFailed, func(s *state.Status) { s.ExitCode = 1; s.Errors = []string{"disk on fire"} }, Fail, "disk on fire"},
		{"partial", state.StatePartial, func(s *state.Status) { s.ExitCode = 3 }, Warn, "no error recorded"},
		{"refused", state.StateRefused, func(s *state.Status) { s.ExitCode = 4; s.RefuseReason = "lock held" }, Warn, "lock held"},
		{"skipped", state.StateSkipped, nil, OK, "skipped"},
	}
	for i, c := range cases {
		id := "2026033" + string(rune('0'+i)) + "T120000Z-docs-aaaa"
		f.status(id, c.state, now.Add(-time.Hour+time.Duration(i)*time.Minute), c.mod)
		expect(t, f.run(), "last run", "docs", c.sev, c.text)
	}
}

func TestLastRunIsOld(t *testing.T) {
	f := newFixture(t)
	f.status("20260101T120000Z-docs-aaaa", state.StateSucceeded, now.Add(-60*24*time.Hour), nil)
	r := f.run()
	expect(t, r, "last run", "docs", Warn, "looks idle")
	if r.Failed() {
		t.Fatal("an old run is a warning")
	}
}

func TestLastRunRunning(t *testing.T) {
	f := newFixture(t)
	f.status("20260401T110000Z-docs-aaaa", state.StateRunning, time.Time{}, nil)
	expect(t, f.run(), "last run", "docs", Warn, "pid 4242 is gone")
	write(t, filepath.Join(f.proc, "4242", "stat"), "x")
	expect(t, f.run(), "last run", "docs", OK, "is running")
}

func TestLocks(t *testing.T) {
	f := newFixture(t)
	lockPath := f.env.Store.LockPath("docs")

	write(t, lockPath, "4242\n") // present, not held, process gone, but the last run finished
	f.status("20260331T120000Z-docs-aaaa", state.StateSucceeded, now.Add(-time.Hour), nil)
	expect(t, f.run(), "lock", "docs", OK, "not held")

	f.status("20260401T110000Z-docs-bbbb", state.StateRunning, time.Time{}, nil) // crashed run
	expect(t, f.run(), "lock", "docs", Warn, "stale")

	write(t, lockPath, "garbage")
	f.status("20260401T110000Z-docs-bbbb", state.StateSucceeded, now, nil)
	expect(t, f.run(), "lock", "docs", OK, "no lock file yet")
}

func TestHeldLocks(t *testing.T) {
	f := newFixture(t)
	mkdir(t, f.env.Store.LocksDir())
	lockPath := f.env.Store.LockPath("docs")
	l, err := acquire(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()

	pid := os.Getpid()
	write(t, filepath.Join(f.proc, strings.TrimSpace(itoa(pid)), "stat"), "x")
	expect(t, f.run(), "lock", "docs", OK, "held by running pid")

	if err := os.Remove(filepath.Join(f.proc, itoa(pid), "stat")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.proc, itoa(pid))); err != nil {
		t.Fatal(err)
	}
	expect(t, f.run(), "lock", "docs", Warn, "not running")
}

func TestLockPathIsASymlink(t *testing.T) {
	f := newFixture(t)
	mkdir(t, f.env.Store.LocksDir())
	if err := os.Symlink(f.src, f.env.Store.LockPath("docs")); err != nil {
		t.Fatal(err)
	}
	expect(t, f.run(), "lock", "docs", Warn, "cannot inspect")
}

func TestCancelledContextStopsJobChecks(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Run(ctx, f.env)
	for _, c := range r.Checks {
		if c.Job != "" {
			t.Fatalf("job check ran after cancellation: %+v", c)
		}
	}
}

func TestRendering(t *testing.T) {
	f := newFixture(t)
	f.free = 0
	f.backup(1, 1<<30)
	r := f.run()
	var text bytes.Buffer
	if err := r.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[ok  ] state directory:", "[FAIL] free space [docs]:", "failure(s)"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, text.String())
		}
	}
	var js bytes.Buffer
	if err := r.WriteJSON(&js); err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(js.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.OK || back.Failures != 1 || back.Version != "v9.9.9-test" || len(back.Checks) != len(r.Checks) {
		t.Fatalf("%+v", back)
	}
	if !strings.Contains(js.String(), `"severity": "fail"`) {
		t.Fatal(js.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestWriteTextReportsWriterErrors(t *testing.T) {
	f := newFixture(t)
	if err := f.run().WriteText(failingWriter{}); err == nil {
		t.Fatal("expected the writer error")
	}
}

func TestDefaultsAreProductionValues(t *testing.T) {
	e := (Env{}).withDefaults()
	if e.SysRoot != "/sys" || e.ProcRoot != "/proc" || e.MountInfo != "/proc/self/mountinfo" || e.Statfs == nil || e.Access == nil || e.Now == nil {
		t.Fatalf("%+v", e)
	}
	dir := t.TempDir()
	if err := e.Access(dir, CanRead); err != nil {
		t.Fatal(err)
	}
}
