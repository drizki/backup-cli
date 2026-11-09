package safedelete

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/drizki/backup-cli/internal/backupset"
)

// remove re-checks the target through a handle on its parent and then
// removes it relative to that handle, so a parent swapped after the check
// cannot redirect the removal. The marker goes first (and is synced), so a
// backup that is only half deleted never still looks committed; read-only
// directories are made owner-writable before they are emptied.
func (g Guard) remove(t target) error {
	if g.beforeRemove != nil {
		g.beforeRemove(t.plan.Path)
	}
	root, err := os.OpenRoot(filepath.Dir(t.plan.Path))
	if err != nil {
		return refuse("cannot open the parent of %s: %v", t.plan.Path, err)
	}
	defer func() { _ = root.Close() }()
	name := filepath.Base(t.plan.Path)
	fi, err := root.Lstat(name)
	if err != nil {
		return refuse("%s changed before removal: %v", t.plan.Path, err)
	}
	if !fi.IsDir() || !os.SameFile(fi, t.fi) {
		return refuse("%s changed type or identity between the check and the removal", t.plan.Path)
	}
	openUp(root, name)
	if err := dropMarker(root, name); err != nil {
		return err
	}
	return root.RemoveAll(name)
}

// dropMarker removes the backup's marker and syncs the directory.
func dropMarker(root *os.Root, name string) error {
	if err := root.Remove(path.Join(name, backupset.MarkerName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	d, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// openUp makes rel and every directory below it owner-readable, -writable
// and -searchable so RemoveAll can empty them. Symlinks are not followed and
// failures are ignored: RemoveAll reports what it cannot remove.
func openUp(root *os.Root, rel string) {
	if !openDir(root, rel) {
		return
	}
	d, err := root.Open(rel)
	if err != nil {
		return
	}
	items, _ := d.ReadDir(-1)
	_ = d.Close()
	for _, it := range items {
		if it.IsDir() {
			openUp(root, path.Join(rel, it.Name()))
		}
	}
}

// openDir makes the single directory rel owner-readable, -writable and
// -searchable and reports whether it is a directory. Symlinks are not followed.
func openDir(root *os.Root, rel string) bool {
	fi, err := root.Lstat(rel)
	if err != nil || !fi.IsDir() {
		return false
	}
	if fi.Mode().Perm()&0o700 != 0o700 {
		_ = root.Chmod(rel, fi.Mode().Perm()|0o700)
	}
	return true
}

// SweepPartial removes from the partial backup directory dir every entry that
// is not in want or whose type differs, and returns their slash-separated
// paths. want maps a path below dir to its type bits (fs.ModeDir,
// fs.ModeSymlink, or 0 for a regular file); keep names top-level entries to
// leave alone. The caller must already have proven that dir is its own
// partial directory. Removal is confined to dir through an os.Root.
func SweepPartial(dir string, want map[string]fs.FileMode, keep ...string) ([]string, error) {
	if fi, err := os.Lstat(dir); err == nil && fi.IsDir() && fi.Mode().Perm()&0o700 != 0o700 {
		_ = os.Chmod(dir, fi.Mode().Perm()|0o700) // a crash after the final chmod may have locked it
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	s := sweeper{root: root, want: want, keep: keep}
	if err := s.dir("."); err != nil {
		return s.removed, err
	}
	return s.removed, nil
}

type sweeper struct {
	root    *os.Root
	want    map[string]fs.FileMode
	keep    []string
	removed []string
}

func (s *sweeper) dir(rel string) error {
	openDir(s.root, rel) // a crashed run may have left a read-only directory
	d, err := s.root.Open(rel)
	if err != nil {
		return err
	}
	items, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return err
	}
	for _, it := range items {
		if err := s.entry(rel, it); err != nil {
			return err
		}
	}
	return nil
}

func (s *sweeper) entry(parent string, it fs.DirEntry) error {
	rel := path.Join(parent, it.Name())
	if parent == "." {
		for _, k := range s.keep {
			if k == it.Name() {
				return nil
			}
		}
	}
	typ := it.Type() & fs.ModeType
	if t, planned := s.want[rel]; planned && t == typ {
		if typ == fs.ModeDir {
			return s.dir(rel)
		}
		return nil
	}
	openUp(s.root, rel)
	if err := s.root.RemoveAll(rel); err != nil {
		return err
	}
	s.removed = append(s.removed, rel)
	return nil
}
