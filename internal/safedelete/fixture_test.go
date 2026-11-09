package safedelete

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/journal"
)

type fixture struct {
	t     *testing.T
	root  string
	src   string
	dst   string
	state string
	info  string
	j     job.Job
	g     Guard
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: root, src: filepath.Join(root, "data"), dst: filepath.Join(root, "bk"), state: filepath.Join(root, "state")}
	for _, d := range []string{f.src, f.dst, f.state} {
		mkdir(t, d)
	}
	f.info = filepath.Join(root, "mountinfo")
	f.mounts("22 1 8:1 / / rw - ext4 /dev/sda1 rw\n")
	f.j = job.Job{Version: 1, Name: "docs", Source: f.src, DstTemplate: filepath.Join(f.dst, "docs-{timestamp}"), Keep: 3}
	f.g = Guard{StateDir: f.state, MountInfo: f.info}
	write(t, filepath.Join(f.src, "a.txt"), "source data")
	return f
}

func (f *fixture) mounts(content string) {
	f.t.Helper()
	write(f.t, f.info, content)
}

// backup creates a committed backup directory with a payload file.
func (f *fixture) backup(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.dst, name)
	mkdir(f.t, filepath.Join(dir, "sub"))
	write(f.t, filepath.Join(dir, "sub", "file.txt"), "payload of "+name)
	m := backupset.Marker{Job: "docs", RunID: "r1", Created: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), FileCount: 7, Bytes: 4096}
	if err := backupset.WriteMarker(dir, m); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

// partial creates a .partial directory with a journal naming jobName.
func (f *fixture) partial(name, jobName string) string {
	f.t.Helper()
	dir := filepath.Join(f.dst, name)
	mkdir(f.t, dir)
	write(f.t, filepath.Join(dir, "half.bin"), "half")
	jr, err := journal.Open(filepath.Join(dir, journal.Name), jobName, "r1", time.Time{})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := jr.Close(); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

func mkdir(t *testing.T, d string) {
	t.Helper()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot lists every path under root (without following symlinks) with a
// content hash, so a test can prove nothing changed.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			fmt.Fprintf(&b, "%s -> %s\n", p, target)
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(p)
			fmt.Fprintf(&b, "%s %x\n", p, sha256.Sum256(data))
		default:
			fmt.Fprintf(&b, "%s dir\n", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// expectRefused asserts a refusal mentioning want and that nothing under the
// fixture root changed.
func (f *fixture) expectRefused(err error, want, before string) {
	f.t.Helper()
	if err == nil {
		f.t.Fatalf("expected a refusal mentioning %q, got success", want)
	}
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), want) {
		f.t.Fatalf("expected a refusal mentioning %q, got: %v", want, err)
	}
	if after := snapshot(f.t, f.root); after != before {
		f.t.Fatalf("the tree changed despite the refusal:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
