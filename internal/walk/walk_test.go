package walk

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/drizki/backup-cli/internal/manifest"
)

func mustWrite(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func byPath(res Result) map[string]manifest.Entry {
	m := map[string]manifest.Entry{}
	for _, e := range res.Entries {
		m[e.Path] = e
	}
	return m
}

func buildTree(t *testing.T) (root, outside string, links map[string]string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), "hello")
	mustWrite(t, filepath.Join(root, "sub", "b.txt"), "bb")
	mustWrite(t, filepath.Join(root, "name with space\nand newline"), "x")
	mustWrite(t, filepath.Join(root, "ünï", "cödé"), "u")
	mustWrite(t, filepath.Join(outside, "secret"), "do not copy")
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	links = map[string]string{"rel": "a.txt", "abs": "/etc/hostname", "dangling": "nowhere", "out": outside, "up": "../x"}
	for n, target := range links {
		if err := os.Symlink(target, filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside, links
}

func TestWalkTree(t *testing.T) {
	root, _, links := buildTree(t)
	res, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(res)
	if m["."].Type != manifest.TypeDir || res.Entries[0].Path != "." {
		t.Fatalf("root entry first: %+v", res.Entries[0])
	}
	for n, target := range links {
		e, ok := m[n]
		if !ok || e.Type != manifest.TypeSymlink || e.LinkTarget != target {
			t.Errorf("symlink %s = %+v", n, e)
		}
	}
	if _, followed := m["out/secret"]; followed {
		t.Error("walk followed a symlinked directory")
	}
	if e := m["empty"]; e.Type != manifest.TypeDir {
		t.Errorf("empty directory missing: %+v", e)
	}
	checkFiles(t, res, m)
	checkParentsFirst(t, res)
}

func checkFiles(t *testing.T, res Result, m map[string]manifest.Entry) {
	t.Helper()
	for _, n := range []string{"a.txt", "sub/b.txt", "name with space\nand newline", "ünï/cödé"} {
		if m[n].Type != manifest.TypeFile {
			t.Errorf("%q missing", n)
		}
	}
	if res.Files != 4 || res.Bytes != 5+2+1+1 {
		t.Errorf("files=%d bytes=%d", res.Files, res.Bytes)
	}
	if m["a.txt"].Size != 5 || m["a.txt"].MtimeNS == 0 || m["a.txt"].Mode != 0o644 {
		t.Errorf("metadata %+v", m["a.txt"])
	}
}

func checkParentsFirst(t *testing.T, res Result) {
	t.Helper()
	seen := map[string]bool{}
	for _, e := range res.Entries {
		if parent := filepath.ToSlash(filepath.Dir(e.Path)); e.Path != "." && !seen[parent] {
			t.Errorf("%s emitted before its parent %s", e.Path, parent)
		}
		seen[e.Path] = true
	}
}

func TestWalkSkipsSpecialFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "regular"), "r")
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(root, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	res, err := Walk(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range res.Skipped {
		kinds[s.Path] = s.Kind
	}
	if kinds["pipe"] != "fifo" || kinds["sock"] != "socket" || len(res.Skipped) != 2 {
		t.Fatalf("skipped: %+v", res.Skipped)
	}
	if _, ok := byPath(res)["pipe"]; ok {
		t.Fatal("fifo listed as an entry")
	}
}

func TestKindOf(t *testing.T) {
	cases := map[os.FileMode]string{
		os.ModeNamedPipe: "fifo", os.ModeSocket: "socket",
		os.ModeDevice | os.ModeCharDevice: "character device", os.ModeDevice: "block device", os.ModeIrregular: "special file",
	}
	for m, want := range cases {
		if got := kindOf(m); got != want {
			t.Errorf("kindOf(%v) = %q, want %q", m, got, want)
		}
	}
}

func TestWalkErrors(t *testing.T) {
	if _, err := Walk(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing root accepted")
	}
	f := filepath.Join(t.TempDir(), "f")
	mustWrite(t, f, "x")
	if _, err := Walk(context.Background(), f); err == nil {
		t.Error("file root accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a"), "a")
	if _, err := Walk(ctx, root); err == nil {
		t.Error("cancelled walk succeeded")
	}
}

func TestWalkUnreadableDirectoryFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions are not enforced")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mustWrite(t, filepath.Join(locked, "f"), "x")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(locked, 0o755) }()
	if _, err := Walk(context.Background(), root); err == nil {
		t.Fatal("unreadable directory silently skipped")
	}
}
