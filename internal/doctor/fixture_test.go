package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/lock"
	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

var now = time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	t    *testing.T
	root string
	src  string
	dst  string
	sys  string
	proc string
	info string
	fake *systemd.Fake
	free uint64
	// writable lists paths Access allows writing; everything is readable.
	writable map[string]bool
	denyRead map[string]bool
	env      Env
	j        job.Job
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: root, src: filepath.Join(root, "data"), dst: filepath.Join(root, "bk"),
		sys: filepath.Join(root, "sys"), proc: filepath.Join(root, "proc"), info: filepath.Join(root, "mountinfo"),
		fake: systemd.NewFake(), free: 100 << 30, writable: map[string]bool{}, denyRead: map[string]bool{}}
	for _, d := range []string{f.src, f.dst, f.sys, f.proc, filepath.Join(root, "state")} {
		mkdir(t, d)
	}
	f.mounts("22 1 8:1 / / rw - ext4 /dev/sda1 rw\n23 22 0:5 / " + f.dst + " rw - xfs /dev/sdb rw\n")
	f.writable[f.dst] = true
	f.writable[filepath.Join(root, "state")] = true
	f.j = job.Job{Version: 1, Name: "docs", Source: f.src, DstTemplate: filepath.Join(f.dst, "docs-{timestamp}"), Keep: 3, Jobs: 4, Scheduled: true, Created: now}
	f.env = Env{
		Store: state.New(filepath.Join(root, "state")), Manager: f.fake, SysRoot: f.sys, ProcRoot: f.proc, MountInfo: f.info,
		Statfs: func(string) (uint64, error) { return f.free, nil },
		Access: func(p string, mode uint32) error {
			if mode == CanWrite && !f.writable[p] {
				return errors.New("denied")
			}
			if mode == CanRead && f.denyRead[p] {
				return errors.New("denied")
			}
			return nil
		},
		Now: func() time.Time { return now }, Version: "v9.9.9-test",
	}
	f.save()
	f.fake.Installed["backup-cli-docs"] = systemd.Unit{Job: "docs"}
	f.fake.Enabled["backup-cli-docs"] = true
	return f
}

func (f *fixture) save() {
	f.t.Helper()
	if err := f.env.Store.SaveJob(f.j); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) mounts(content string) {
	f.t.Helper()
	write(f.t, f.info, content)
}

func (f *fixture) backup(n int, bytes int64) string {
	f.t.Helper()
	dir := filepath.Join(f.dst, fmt.Sprintf("docs-202603%02dT000000Z", n))
	mkdir(f.t, dir)
	m := backupset.Marker{Job: "docs", RunID: "r", Created: time.Date(2026, 3, n, 0, 0, 0, 0, time.UTC), Bytes: bytes}
	if err := backupset.WriteMarker(dir, m); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

func (f *fixture) status(id, st string, finished time.Time, mod func(*state.Status)) {
	f.t.Helper()
	s := state.Status{RunID: id, Job: "docs", State: st, Started: finished.Add(-time.Minute), Finished: finished, PID: 4242}
	if mod != nil {
		mod(&s)
	}
	if err := f.env.Store.WriteStatus(s); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) run() Report { return Run(context.Background(), f.env) }

// find returns the check with the given name for the job ("" for global).
func find(t *testing.T, r Report, name, jobName string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name && c.Job == jobName {
			return c
		}
	}
	t.Fatalf("no check %q for %q in %+v", name, jobName, r.Checks)
	return Check{}
}

func expect(t *testing.T, r Report, name, jobName string, sev Severity, detail string) {
	t.Helper()
	c := find(t, r, name, jobName)
	if c.Severity != sev || !strings.Contains(c.Detail, detail) {
		t.Fatalf("%s/%s: got %s %q, want %s containing %q", name, jobName, c.Severity, c.Detail, sev, detail)
	}
}

func mkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func acquire(path string) (*lock.Lock, error) { return lock.Acquire(path) }
