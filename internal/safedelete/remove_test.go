package safedelete

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/journal"
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

func TestSweepPartialRemovesWhatIsNotPlanned(t *testing.T) {
	f := newFixture(t)
	dir := f.partial("docs-20260301T000000Z.partial", "docs")
	write(t, filepath.Join(dir, "keep.txt"), "k")
	mkdir(t, filepath.Join(dir, "d"))
	write(t, filepath.Join(dir, "d", "x.txt"), "x")
	write(t, filepath.Join(dir, "d", "stray.txt"), "s")
	write(t, filepath.Join(dir, "gone.txt"), "g")
	mkdir(t, filepath.Join(dir, "olddir", "inner"))
	readOnlyTree(t, filepath.Join(dir, "olddir"))
	mkdir(t, filepath.Join(dir, "wasfile", "child"))
	if err := os.Symlink("keep.txt", filepath.Join(dir, "ln")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("keep.txt", filepath.Join(dir, "wasdir")); err != nil {
		t.Fatal(err)
	}
	want := map[string]fs.FileMode{
		"keep.txt": 0, "d": fs.ModeDir, "d/x.txt": 0, "wasfile": 0, "wasdir": fs.ModeDir, "ln": fs.ModeSymlink,
	}
	removed, err := SweepPartial(dir, want, "half.bin", journal.Name)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(removed)
	if got := slices.Compare(removed, []string{"d/stray.txt", "gone.txt", "olddir", "wasdir", "wasfile"}); got != 0 {
		t.Fatalf("removed %v", removed)
	}
	for _, p := range []string{"keep.txt", "d/x.txt", "half.bin", journal.Name, "ln"} {
		if _, err := os.Lstat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(dir, "ln")); err != nil || target != "keep.txt" {
		t.Errorf("symlink changed: %q %v", target, err)
	}
}

func TestSweepPartialOpensEveryPlannedDirectoryItVisits(t *testing.T) {
	f := newFixture(t)
	dir := f.partial("docs-20260301T000000Z.partial", "docs")
	mkdir(t, filepath.Join(dir, "ro", "deep"))
	write(t, filepath.Join(dir, "ro", "deep", "stray.txt"), "s")
	readOnlyTree(t, dir) // leaves ro and ro/deep read-only
	want := map[string]fs.FileMode{
		"ro": fs.ModeDir, "ro/g.txt": 0, "ro/deep": fs.ModeDir, "ro/deep/f.txt": 0,
	}
	removed, err := SweepPartial(dir, want, "half.bin", journal.Name)
	if err != nil || !slices.Equal(removed, []string{"ro/deep/stray.txt"}) {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	for _, p := range []string{"ro", "ro/deep"} {
		fi, err := os.Lstat(filepath.Join(dir, p))
		if err != nil || fi.Mode().Perm()&0o700 != 0o700 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
}

func TestSweepPartialStaysInsideTheDirectory(t *testing.T) {
	f := newFixture(t)
	dir := f.partial("docs-20260301T000000Z.partial", "docs")
	outside := filepath.Join(f.root, "outside")
	mkdir(t, outside)
	write(t, filepath.Join(outside, "precious"), "p")
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	removed, err := SweepPartial(dir, map[string]fs.FileMode{}, journal.Name)
	if err != nil || !slices.Contains(removed, "escape") {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Fatalf("the sweep followed a symlink out of the directory: %v", err)
	}
}

func TestSweepPartialErrors(t *testing.T) {
	if _, err := SweepPartial(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("expected an error for a missing directory")
	}
	f := newFixture(t)
	dir := f.partial("docs-20260301T000000Z.partial", "docs")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	// A directory that lost its permissions is made usable again, not an error.
	if _, err := SweepPartial(dir, nil, journal.Name); err != nil {
		t.Fatal(err)
	}
}
