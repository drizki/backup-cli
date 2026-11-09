package safedelete

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/mount"
	"github.com/drizki/backup-cli/internal/pathguard"
)

// check applies every guard to dir for job j and returns the target.
func (g Guard) check(j job.Job, dir string, partial bool) (target, error) {
	var t target
	if g.StateDir == "" {
		return t, refuse("no state directory configured for the guard")
	}
	path, err := cleanTarget(dir)
	if err != nil {
		return t, err
	}
	if t.fi, err = lstatDir(path); err != nil {
		return t, err
	}
	if err := checkLocation(j, path, partial); err != nil {
		return t, err
	}
	t.plan = Plan{Job: j.Name, Path: path, Partial: partial}
	if err := checkOwner(j, &t.plan); err != nil {
		return t, err
	}
	real, err := pathguard.Resolve(path)
	if err != nil {
		return t, refuse("cannot resolve %s: %v", path, err)
	}
	if err := checkApart("the job's source", j.Source, real, t.fi); err != nil {
		return t, err
	}
	if err := checkApart("the state directory", g.StateDir, real, t.fi); err != nil {
		return t, err
	}
	return t, g.checkMounts(real)
}

// cleanTarget requires an absolute path without ".." elements and not "/".
func cleanTarget(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", refuse("%q is not an absolute path", dir)
	}
	for _, part := range strings.Split(dir, string(filepath.Separator)) {
		if part == ".." {
			return "", refuse("%q contains a .. element", dir)
		}
	}
	path := filepath.Clean(dir)
	if path == string(filepath.Separator) {
		return "", refuse("refusing to delete the filesystem root")
	}
	return path, nil
}

// lstatDir requires a real directory; a symlink is refused, never followed.
func lstatDir(path string) (os.FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, refuse("%s: %v", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, refuse("%s is a symlink; symlinks are never followed or deleted", path)
	}
	if !fi.IsDir() {
		return nil, refuse("%s is not a directory", path)
	}
	return fi, nil
}

// checkLocation requires the target to sit directly in the job's destination
// parent (spelled the way the job spells it) with a name from its template.
func checkLocation(j job.Job, path string, partial bool) error {
	parent := filepath.Clean(j.DstParent())
	if filepath.Dir(path) != parent {
		return refuse("%s is not directly inside the job's destination parent %s", path, parent)
	}
	name := filepath.Base(path)
	if partial != strings.HasSuffix(name, backupset.PartialSuffix) {
		return refuse("%s: partial directories need DeletePartial, committed backups must not end in %s", path, backupset.PartialSuffix)
	}
	if !j.MatchesName(strings.TrimSuffix(name, backupset.PartialSuffix)) {
		return refuse("%s does not match the job's destination template", name)
	}
	return nil
}

// checkOwner proves the directory belongs to the job: a valid marker for a
// committed backup, a journal naming the job for a partial one.
func checkOwner(j job.Job, p *Plan) error {
	if p.Partial {
		rep, err := journal.Read(filepath.Join(p.Path, journal.Name))
		if err != nil {
			return refuse("%s has no readable journal: %v", p.Path, err)
		}
		if rep.Job != j.Name {
			return refuse("the journal of %s belongs to job %q, not %q", p.Path, rep.Job, j.Name)
		}
		return nil
	}
	m, err := backupset.ReadMarker(p.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return refuse("%s has no %s marker; it is not a backup made by this tool", p.Path, backupset.MarkerName)
	}
	if err != nil {
		return refuse("%s has an unusable marker: %v", p.Path, err)
	}
	if m.Job != j.Name {
		return refuse("the marker of %s belongs to job %q, not %q", p.Path, m.Job, j.Name)
	}
	p.Files, p.Bytes = m.FileCount, m.Bytes
	return nil
}

// checkApart refuses when the target equals, contains or lies inside other,
// by resolved path and by inode (which also catches bind-style aliases).
func checkApart(label, other, real string, fi os.FileInfo) error {
	ro, err := pathguard.Resolve(other)
	if err != nil {
		return refuse("cannot resolve %s %s: %v", label, other, err)
	}
	if pathguard.Contains(real, ro) || pathguard.Contains(ro, real) {
		return refuse("%s equals, contains or lies inside %s (%s)", real, label, ro)
	}
	if fi2, err := os.Stat(ro); err == nil && (os.SameFile(fi, fi2) || ancestorOf(fi, ro) || ancestorOf(fi2, real)) {
		return refuse("%s is the same directory as, or contains, or lies inside %s (by inode)", real, label)
	}
	return nil
}

// ancestorOf reports whether fi is a proper ancestor directory of p.
func ancestorOf(fi os.FileInfo, p string) bool {
	for cur := filepath.Dir(p); ; cur = filepath.Dir(cur) {
		if st, err := os.Stat(cur); err == nil && os.SameFile(fi, st) {
			return true
		}
		if cur == filepath.Dir(cur) {
			return false
		}
	}
}

// checkMounts refuses mount points: the target itself, or any inside it.
func (g Guard) checkMounts(real string) error {
	points, err := mount.Inside(g.mountInfo(), real)
	if err != nil {
		return refuse("cannot read the mount table: %v", err)
	}
	if len(points) > 0 {
		return refuse("%s is or contains the mount point %s", real, points[0])
	}
	return nil
}
