package safedelete

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/drizki/backup-cli/internal/backupset"
)

// readOnlyTree makes dir/ro (0555) holding a read-only subdirectory (0500),
// the shape of a copied module cache.
func readOnlyTree(t *testing.T, dir string) {
	t.Helper()
	deep := filepath.Join(dir, "ro", "deep")
	mkdir(t, deep)
	write(t, filepath.Join(deep, "f.txt"), "x")
	write(t, filepath.Join(dir, "ro", "g.txt"), "y")
	for _, d := range []string{deep, filepath.Join(dir, "ro")} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { // let t.TempDir clean up if the test failed halfway
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, _ error) error {
			if d != nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
}

func TestDeleteRemovesAReadOnlyDirectoryTree(t *testing.T) {
	f := newFixture(t)
	dir := f.backup("docs-20260301T000000Z")
	readOnlyTree(t, dir)
	if err := f.g.Delete(f.j, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("backup still exists: %v", err)
	}
}

func TestDeletePartialRemovesAReadOnlyDirectoryTree(t *testing.T) {
	f := newFixture(t)
	dir := f.partial("docs-20260301T000000Z.partial", "docs")
	readOnlyTree(t, dir)
	if err := f.g.DeletePartial(f.j, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("partial still exists: %v", err)
	}
}

func TestDropMarkerMakesTheDirectoryUncommitted(t *testing.T) {
	f := newFixture(t)
	dir := f.backup("docs-20260301T000000Z")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	root, err := os.OpenRoot(f.dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	openUp(root, filepath.Base(dir))
	if err := dropMarker(root, filepath.Base(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := backupset.ReadMarker(dir); !os.IsNotExist(err) {
		t.Fatalf("marker still readable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "file.txt")); err != nil {
		t.Fatalf("payload was touched: %v", err)
	}
	if err := dropMarker(root, "missing"); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
}
