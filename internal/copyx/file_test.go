package copyx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func snapshot(t *testing.T, p string) (string, os.FileInfo) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), fi
}

func TestCopyFilePreservesDataAndMetadata(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	data := bytes.Repeat([]byte("payload"), 300000)
	if err := os.WriteFile(src, data, 0o640); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(1_600_000_000, 123_456_789)
	if err := os.Chtimes(src, mt, mt); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, os.ModeSetuid|0o750); err != nil {
		t.Fatal(err)
	}
	beforeData, beforeInfo := snapshot(t, src)
	dst := filepath.Join(t.TempDir(), "dst")
	f := mustOpen(t, src)
	res, err := CopyFile(context.Background(), dst, f, FileOpts{Buf: make([]byte, 64*1024), Chown: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Size != int64(len(data)) || res.SHA256 != hexSum(data) {
		t.Fatalf("%+v", res)
	}
	got, di := snapshot(t, dst)
	if got != beforeData {
		t.Fatal("data differs")
	}
	if di.ModTime().UnixNano() != mt.UnixNano() {
		t.Errorf("mtime %v want %v", di.ModTime().UnixNano(), mt.UnixNano())
	}
	if MetaOf(di).Mode != 0o4750 {
		t.Errorf("mode %o", MetaOf(di).Mode)
	}
	_, afterInfo := snapshot(t, src)
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) || afterInfo.Mode() != beforeInfo.Mode() {
		t.Error("source metadata changed")
	}
}

func TestCopyFileExplicitMetaAndTruncate(t *testing.T) {
	src := writeTemp(t, []byte("new content"))
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.WriteFile(dst, bytes.Repeat([]byte("old "), 100), 0o600); err != nil {
		t.Fatal(err)
	}
	m := Meta{Mode: 0o600, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), MtimeNS: 42_000_000_007}
	res, err := CopyFile(context.Background(), dst, mustOpen(t, src), FileOpts{Buf: make([]byte, 8), Meta: &m, Chown: true})
	if err != nil || res.Meta != m {
		t.Fatalf("%+v %v", res, err)
	}
	got, di := snapshot(t, dst)
	if got != "new content" || di.ModTime().UnixNano() != m.MtimeNS || MetaOf(di).Mode != 0o600 {
		t.Fatalf("%q %v %o", got, di.ModTime(), MetaOf(di).Mode)
	}
}

func TestCopyFileDetectsChangeDuringCopy(t *testing.T) {
	src := writeTemp(t, []byte("stable"))
	dst := filepath.Join(t.TempDir(), "dst")
	hook := func() {
		later := time.Now().Add(time.Hour)
		_ = os.Chtimes(src, later, later)
	}
	res, err := CopyFile(context.Background(), dst, mustOpen(t, src), FileOpts{Buf: make([]byte, 8), AfterCopy: hook})
	if err != nil || !res.Changed {
		t.Fatalf("mtime change not detected: %+v %v", res, err)
	}
	grow := func() {
		f, _ := os.OpenFile(src, os.O_WRONLY|os.O_APPEND, 0)
		_, _ = f.WriteString("more")
		_ = f.Close()
	}
	res, err = CopyFile(context.Background(), dst, mustOpen(t, src), FileOpts{Buf: make([]byte, 8), AfterCopy: grow})
	if err != nil || !res.Changed {
		t.Fatalf("growth not detected: %+v %v", res, err)
	}
}

func TestCopyFileNeverWritesThroughSymlink(t *testing.T) {
	src := writeTemp(t, []byte("data"))
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := os.Symlink(victim, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyFile(context.Background(), dst, mustOpen(t, src), FileOpts{Buf: make([]byte, 8)}); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Fatalf("victim modified: %q", b)
	}
}

func TestCopyFileErrors(t *testing.T) {
	src := writeTemp(t, []byte("data"))
	missingDir := filepath.Join(t.TempDir(), "no", "dst")
	if _, err := CopyFile(context.Background(), missingDir, mustOpen(t, src), FileOpts{Buf: make([]byte, 8)}); err == nil {
		t.Fatal("missing destination directory accepted")
	}
	closed := mustOpen(t, src)
	_ = closed.Close()
	if _, err := CopyFile(context.Background(), filepath.Join(t.TempDir(), "d"), closed, FileOpts{Buf: make([]byte, 8)}); err == nil {
		t.Fatal("closed source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dst := filepath.Join(t.TempDir(), "d")
	_, err := CopyFile(ctx, dst, mustOpen(t, src), FileOpts{Buf: make([]byte, 8)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Lstat(dst); err != nil {
		t.Fatal("the partly written file must be left in place, not removed")
	}
}

func TestApplyErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, chown := range []bool{false, true} {
		if err := Apply(missing, Meta{Mode: 0o644}, chown); err == nil {
			t.Errorf("chown=%v: missing path accepted", chown)
		}
	}
	if os.Geteuid() != 0 {
		p := writeTemp(t, []byte("x"))
		if err := Apply(p, Meta{Mode: 0o644, UID: 0, GID: 0}, true); err == nil {
			t.Error("chown to root succeeded as non-root")
		}
	}
}

func TestMakeDir(t *testing.T) {
	base := t.TempDir()
	d := filepath.Join(base, "d")
	if err := MakeDir(d); err != nil {
		t.Fatal(err)
	}
	if err := MakeDir(d); err != nil {
		t.Fatalf("existing dir: %v", err)
	}
	if err := os.Chmod(d, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := MakeDir(d); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(d); fi.Mode().Perm()&0o700 != 0o700 {
		t.Fatalf("directory not made writable: %v", fi.Mode())
	}
	file := filepath.Join(base, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MakeDir(file); !errors.Is(err, ErrConflict) {
		t.Fatalf("file in the way: %v", err)
	}
	link := filepath.Join(base, "l")
	if err := os.Symlink(d, link); err != nil {
		t.Fatal(err)
	}
	if err := MakeDir(link); !errors.Is(err, ErrConflict) {
		t.Fatalf("symlink in the way: %v", err)
	}
	if err := MakeDir(filepath.Join(base, "x", "y")); err == nil {
		t.Fatal("missing parent accepted")
	}
}

func TestSymlinkVerbatimAndReplace(t *testing.T) {
	base := t.TempDir()
	m := Meta{MtimeNS: 1_500_000_000_000_000_123, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
	for _, target := range []string{"relative/target", "../up", "/abs/dangling", "nonexistent"} {
		l := filepath.Join(base, "l")
		if err := Symlink(l, target, m, true); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		got, err := os.Readlink(l)
		if err != nil || got != target {
			t.Fatalf("target %q, read %q (%v)", target, got, err)
		}
		fi, _ := os.Lstat(l)
		if fi.ModTime().UnixNano() != m.MtimeNS {
			t.Errorf("symlink mtime %d", fi.ModTime().UnixNano())
		}
	}
	if err := Symlink(filepath.Join(base, "l"), "nonexistent", m, false); err != nil {
		t.Fatalf("identical existing link: %v", err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 {
		t.Fatalf("temporary links left behind: %v", entries)
	}
	file := filepath.Join(base, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Symlink(file, "x", m, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("file in the way: %v", err)
	}
	if err := Symlink(filepath.Join(base, "no", "l"), "x", m, false); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err := lutimes("bad\x00path", 1); err == nil {
		t.Fatal("NUL in path accepted")
	}
}
