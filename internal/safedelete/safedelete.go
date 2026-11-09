// Package safedelete is the only place in the code base that deletes backup
// data. Every deletion passes the guards in guards.go, and the target is
// re-checked immediately before removal. Symlinks are refused, never
// followed. A repository test (guard_test.go) fails if deletion calls appear
// outside this package, apart from a short, justified allowlist.
package safedelete

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/mount"
)

// ErrRefused wraps every refusal; nothing was deleted when it is returned.
var ErrRefused = errors.New("deletion refused")

func refuse(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// Guard holds the facts the guards compare against.
type Guard struct {
	// StateDir is the state directory; it and everything in it is protected.
	StateDir string
	// MountInfo is the mountinfo table; empty means /proc/self/mountinfo.
	MountInfo string

	// beforeRemove lets tests change the world between check and removal.
	beforeRemove func(path string)
}

// Plan describes what a deletion would remove. Files and Bytes come from the
// backup's marker (zero for a .partial directory).
type Plan struct {
	Job     string
	Path    string
	Files   int64
	Bytes   int64
	Partial bool
}

// target is a directory that passed every guard.
type target struct {
	plan Plan
	fi   os.FileInfo
}

func (g Guard) mountInfo() string {
	if g.MountInfo == "" {
		return mount.DefaultInfo
	}
	return g.MountInfo
}

// Plan runs every guard for a committed backup of j and reports what Delete
// would remove. It deletes nothing.
func (g Guard) Plan(j job.Job, dir string) (Plan, error) {
	t, err := g.check(j, dir, false)
	return t.plan, err
}

// PlanPartial is Plan for a ".partial" directory.
func (g Guard) PlanPartial(j job.Job, dir string) (Plan, error) {
	t, err := g.check(j, dir, true)
	return t.plan, err
}

// Delete removes one committed backup of j after all guards pass.
func (g Guard) Delete(j job.Job, dir string) error {
	t, err := g.check(j, dir, false)
	if err != nil {
		return err
	}
	return g.remove(t)
}

// DeletePartial removes a ".partial" directory whose journal names j.
func (g Guard) DeletePartial(j job.Job, dir string) error {
	t, err := g.check(j, dir, true)
	if err != nil {
		return err
	}
	return g.remove(t)
}

// remove re-checks the target through a handle on its parent and then
// removes it relative to that handle, so a parent swapped after the check
// cannot redirect the removal.
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
	return root.RemoveAll(name)
}
