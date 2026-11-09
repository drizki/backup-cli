package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func entry(p string) Entry {
	return Entry{Path: p, Size: 3, MtimeNS: 9, SHA256: strings.Repeat("a", 64)}
}

func TestAppendAndRead(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	j, err := Open(p, "job1", "run1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, n := range []string{"a", "b/c", "d e"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := j.Add(entry(n)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	rep, err := Read(p)
	if err != nil || rep.Job != "job1" || len(rep.Entries) != 3 || rep.Bad != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep.Entries["b/c"] != entry("b/c") {
		t.Fatalf("%+v", rep.Entries["b/c"])
	}
}

func TestReopenAppendsHeaderAndKeepsEntries(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	j, _ := Open(p, "job1", "run1", time.Time{})
	_ = j.Add(entry("one"))
	_ = j.Close()
	j, err := Open(p, "job1", "run2", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	_ = j.Add(entry("two"))
	_ = j.Close()
	rep, err := Read(p)
	if err != nil || len(rep.Entries) != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestTornAndCorruptLinesTolerated(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	j, _ := Open(p, "job1", "run1", time.Time{})
	_ = j.Add(entry("good"))
	_ = j.Close()
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("garbage line\n{\"t\":\"f\",\"path\":\"torn\",\"si")
	_ = f.Close()
	j, err := Open(p, "job1", "run2", time.Time{}) // must terminate the torn line first
	if err != nil {
		t.Fatal(err)
	}
	_ = j.Add(entry("after"))
	_ = j.Close()
	rep, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rep.Entries["good"]; !ok {
		t.Error("lost an entry before the damage")
	}
	if _, ok := rep.Entries["after"]; !ok {
		t.Error("the entry appended after the torn line was swallowed")
	}
	if _, ok := rep.Entries["torn"]; ok {
		t.Error("torn entry accepted")
	}
	if rep.Bad != 2 {
		t.Errorf("bad lines %d, want 2", rep.Bad)
	}
}

func TestRejectsBadRecords(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	body := `{"t":"h","job":"j"}
{"t":"f","path":"","size":1,"mtime_ns":1,"sha256":"` + strings.Repeat("b", 64) + `"}
{"t":"f","path":"short","size":1,"mtime_ns":1,"sha256":"abc"}
{"t":"x"}
{"t":"f","path":"ok","size":1,"mtime_ns":1,"sha256":"` + strings.Repeat("b", 64) + `"}
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Read(p)
	if err != nil || rep.Bad != 3 || len(rep.Entries) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestForeignJobAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), Name)
	a, _ := Open(p, "job1", "r", time.Time{})
	_ = a.Close()
	b, _ := Open(p, "job2", "r", time.Time{})
	_ = b.Close()
	if _, err := Read(p); !errors.Is(err, ErrForeign) {
		t.Fatalf("mixed jobs: %v", err)
	}
	if _, err := Read(filepath.Join(t.TempDir(), "none")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}

func TestOpenRefusesSymlinkAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(link, "j", "r", time.Time{}); err == nil {
		t.Error("opened through a symlink")
	}
	if _, err := Open(filepath.Join(dir, "no", "such"), "j", "r", time.Time{}); err == nil {
		t.Error("missing directory accepted")
	}
}

func TestDiscard(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, Name)
	j, _ := Open(p, "j", "r", time.Time{})
	_ = j.Close()
	if err := Discard(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal still present")
	}
	if err := Discard(p); err == nil {
		t.Error("missing journal discarded without error")
	}
	d := filepath.Join(dir, "dir")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Discard(d); err == nil {
		t.Error("a directory was discarded")
	}
	if _, err := os.Stat(d); err != nil {
		t.Error("directory vanished")
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), Name), "j", "r", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Add(entry("late")); err == nil {
		t.Error("Add after Close succeeded")
	}
	if err := j.Sync(); err == nil {
		t.Error("Sync after Close succeeded")
	}
	if err := j.Close(); err == nil {
		t.Error("second Close succeeded")
	}
}

func TestOpenOnDirectoryFails(t *testing.T) {
	if _, err := Open(t.TempDir(), "j", "r", time.Time{}); err == nil {
		t.Fatal("a directory was opened as a journal")
	}
}

func TestReadDirectoryFails(t *testing.T) {
	if _, err := Read(t.TempDir()); err == nil {
		t.Fatal("reading a directory succeeded")
	}
}

func TestTrustFollowsCleanStops(t *testing.T) {
	p := filepath.Join(t.TempDir(), "j")
	a, _ := Open(p, "job1", "runA", time.Unix(100, 0))
	_ = a.Add(entry("one"))
	if err := a.CleanStop(); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	b, _ := Open(p, "job1", "runB", time.Unix(200, 0))
	_ = b.Add(entry("two")) // runB is killed: no clean stop
	_ = b.Close()
	rep, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Trusted("one") || rep.Trusted("two") {
		t.Fatalf("trust: one=%v two=%v", rep.Trusted("one"), rep.Trusted("two"))
	}
	if !rep.Started.Equal(time.Unix(100, 0)) {
		t.Fatalf("started %v, want the earliest header", rep.Started)
	}
	c, _ := Open(p, "job1", "runC", time.Time{})
	_ = c.Add(entry("two")) // verified again by runC, which stops cleanly
	_ = c.CleanStop()
	_ = c.Close()
	if rep, _ = Read(p); !rep.Trusted("two") || !rep.Trusted("one") {
		t.Fatal("a re-journaled entry takes the trust of its newest session")
	}
}

func TestCleanStopWriteFailure(t *testing.T) {
	j, err := Open(filepath.Join(t.TempDir(), "j"), "job1", "r", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	_ = j.f.Close()
	if err := j.CleanStop(); err == nil {
		t.Fatal("expected a write error")
	}
}
