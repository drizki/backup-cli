package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/state"
)

// fixture is a temp source tree, destination parent, state dir and engine.
type fixture struct {
	t     *testing.T
	root  string
	src   string
	dst   string
	store state.Store
	j     job.Job
	e     *Engine
	out   *bytes.Buffer
	now   time.Time
	step  time.Duration
}

var baseTime = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: root, src: filepath.Join(root, "data"), dst: filepath.Join(root, "bk"),
		store: state.New(filepath.Join(root, "state")), out: &bytes.Buffer{}, now: baseTime, step: time.Hour}
	for _, d := range []string{f.src, f.dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.j = job.Job{Version: 1, Name: "docs", Source: f.src, DstTemplate: filepath.Join(f.dst, "docs-{timestamp}"),
		Keep: 3, Jobs: 4, VerifySamplePct: 2, Created: baseTime}
	f.e = &Engine{Now: func() time.Time { return f.now }}
	f.save()
	f.populate()
	return f
}

func (f *fixture) save() {
	f.t.Helper()
	if err := f.store.SaveJob(f.j); err != nil {
		f.t.Fatal(err)
	}
}

func pattern(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i*7+i>>8)
	}
	return b
}

// populate writes the standard tree with fixed, nanosecond-precise mtimes.
func (f *fixture) populate() {
	f.put("a.txt", []byte("alpha"))
	f.put("sub/b.bin", pattern(2_500_000, 3))
	f.put("sub/deep/c.txt", []byte("charlie"))
	f.put("name with space.txt", []byte("spaced"))
	f.put("new\nline.txt", []byte("newline name"))
	f.put("ünï/cödé.txt", []byte("unicode"))
	f.mkdir("empty")
	f.link("link-rel", "a.txt")
	f.link("dangling", "no/such/target")
	f.stamp()
}

func (f *fixture) path(rel string) string { return filepath.Join(f.src, filepath.FromSlash(rel)) }

func (f *fixture) put(rel string, data []byte) {
	f.t.Helper()
	p := f.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) mkdir(rel string) {
	f.t.Helper()
	if err := os.MkdirAll(f.path(rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) link(rel, target string) {
	f.t.Helper()
	if err := os.Symlink(target, f.path(rel)); err != nil {
		f.t.Fatal(err)
	}
}

// stamp gives every file and directory a distinct fixed mtime, directories
// last (children first) so their times survive.
func (f *fixture) stamp() {
	f.t.Helper()
	var paths []string
	_ = filepath.WalkDir(f.src, func(p string, d os.DirEntry, _ error) error {
		paths = append(paths, p)
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for i, p := range paths {
		ts := time.Unix(1_700_000_000+int64(i)*60, int64(i)*1_111_111+7)
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			lutimes(f.t, p, ts)
			continue
		}
		if err := os.Chtimes(p, ts, ts); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *fixture) request() cli.EngineRequest {
	return cli.EngineRequest{Store: f.store, Job: f.j, VerifySample: -1, Stdout: f.out, Stderr: f.out}
}

// run advances the clock and runs the job.
func (f *fixture) run(mods ...func(*cli.EngineRequest)) error {
	return f.runCtx(context.Background(), mods...)
}

func (f *fixture) runCtx(ctx context.Context, mods ...func(*cli.EngineRequest)) error {
	f.t.Helper()
	f.now = f.now.Add(f.step)
	req := f.request()
	for _, m := range mods {
		m(&req)
	}
	return f.e.Run(ctx, req)
}

func (f *fixture) mustRun(mods ...func(*cli.EngineRequest)) {
	f.t.Helper()
	if err := f.run(mods...); err != nil {
		f.t.Fatalf("run failed: %v\nlog:\n%s", err, f.out.String())
	}
}

func (f *fixture) expectCode(want int, err error) {
	f.t.Helper()
	if got := exitcode.Code(err); got != want {
		f.t.Fatalf("exit code %d (%v), want %d\nlog:\n%s", got, err, want, f.out.String())
	}
}

func (f *fixture) lastStatus() state.Status {
	f.t.Helper()
	runs, errs := f.store.ListRuns(f.j.Name)
	if len(runs) == 0 || len(errs) > 0 {
		f.t.Fatalf("no readable runs: %v", errs)
	}
	return runs[0]
}

func (f *fixture) committed() []backupset.Backup {
	f.t.Helper()
	all, err := backupset.Scan(f.j)
	if err != nil {
		f.t.Fatal(err)
	}
	return backupset.OnlyCommitted(all)
}

func (f *fixture) latest() string {
	f.t.Helper()
	c := f.committed()
	if len(c) == 0 {
		f.t.Fatal("no committed backup")
	}
	return c[len(c)-1].Path
}

func (f *fixture) partials() []string {
	f.t.Helper()
	m, _ := filepath.Glob(filepath.Join(f.dst, "*"+backupset.PartialSuffix))
	return m
}

// node is the comparable description of one tree entry.
type node struct {
	Typ      string
	Mode     uint32
	UID, GID uint32
	MtimeNS  int64
	Size     int64
	Sum      string
	Link     string
}

func snap(t *testing.T, root string, skip ...string) map[string]node {
	t.Helper()
	out := map[string]node{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for _, s := range skip {
			if rel == s || strings.HasPrefix(rel, s+"/") {
				return nil
			}
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := fi.Sys().(*syscall.Stat_t)
		n := node{Mode: st.Mode, UID: st.Uid, GID: st.Gid, MtimeNS: fi.ModTime().UnixNano()}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			n.Typ = "link"
			n.Link, _ = os.Readlink(p)
		case fi.IsDir():
			n.Typ = "dir"
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			n.Typ, n.Size, n.Sum = "file", fi.Size(), hex.EncodeToString(h[:])
		default:
			n.Typ = "special"
		}
		out[rel] = n
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diffSnaps(a, b map[string]node) string {
	var msgs []string
	for k, v := range a {
		w, ok := b[k]
		switch {
		case !ok:
			msgs = append(msgs, "only in first: "+k)
		case v != w:
			msgs = append(msgs, fmt.Sprintf("%s differs: %+v vs %+v", k, v, w))
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			msgs = append(msgs, "only in second: "+k)
		}
	}
	sort.Strings(msgs)
	return strings.Join(msgs, "\n")
}

// backupMeta lists names that exist only in backups, not in the source.
var backupMeta = []string{"MANIFEST.sha256", "MANIFEST.tsv", ".backup-cli.json"}

// sameAsSource asserts a backup directory mirrors the source tree.
func (f *fixture) sameAsSource(dir string) {
	f.t.Helper()
	want := snap(f.t, f.src)
	got := snap(f.t, dir, backupMeta...)
	// the root's mtime changes when the manifests are added; it is restored last
	if d := diffSnaps(want, got); d != "" {
		f.t.Fatalf("backup differs from source:\n%s", d)
	}
}

func hasWarning(st state.Status, sub string) bool {
	for _, w := range st.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func flipByte(t *testing.T, p string) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)/2] ^= 0xff
	if err := os.WriteFile(p, b, fi.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

// lutimes sets a symlink's own timestamps.
func lutimes(t *testing.T, path string, ts time.Time) {
	t.Helper()
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	tv := [2]syscall.Timespec{syscall.NsecToTimespec(ts.UnixNano()), syscall.NsecToTimespec(ts.UnixNano())}
	dirfd := -100
	if _, _, e := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(dirfd), uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&tv[0])), 0x100, 0, 0); e != 0 {
		t.Fatal(e)
	}
}
