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
	fi, err := root.Lstat(rel)
	if err != nil || !fi.IsDir() {
		return
	}
	if fi.Mode().Perm()&0o700 != 0o700 {
		_ = root.Chmod(rel, fi.Mode().Perm()|0o700)
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
