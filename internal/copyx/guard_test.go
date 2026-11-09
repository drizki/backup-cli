package copyx

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbidden are calls that would let the kernel move data or share extents
// behind this package's back.
var forbidden = []string{"io.Copy", "copy_file_range", "CopyFileRange", "FICLONE", "Reflink", "os.Link(", "syscall.Link(", "unix.Link(", ".ReadFrom(", ".WriteTo(", "Sendfile", "sendfile", "Splice("}

// symlinkAllowed lists the only directory allowed to create symlinks.
const symlinkAllowed = "internal/copyx"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// TestNoCopyOffloadOrLinksInSource scans every non-test Go file of the repo.
func TestNoCopyOffloadOrLinksInSource(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked++
		src := string(b)
		for _, bad := range forbidden {
			if strings.Contains(src, bad) {
				t.Errorf("%s uses %q", rel, bad)
			}
		}
		for _, call := range []string{"os.Symlink(", "syscall.Symlink(", "unix.Symlink("} {
			if strings.Contains(src, call) && !strings.HasPrefix(filepath.ToSlash(rel), symlinkAllowed) {
				t.Errorf("%s creates symlinks with %s outside %s", rel, call, symlinkAllowed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("scanned only %d files; wrong root %s?", checked, root)
	}
}
