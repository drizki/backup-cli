package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/state"
)

// resolveBackup turns a backup argument into a directory path. It accepts a
// path to an existing directory or the "<job>/<name>" id shown by
// `list backups`.
func (a *app) resolveBackup(arg string) (string, error) {
	if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
		return filepath.Clean(arg), nil
	}
	jobName, name, ok := strings.Cut(arg, "/")
	if !ok || jobName == "" || name == "" || strings.Contains(name, "/") || name == ".." || name == "." {
		return "", exitcode.Usagef("%q is neither a directory nor a <job>/<name> backup id", arg)
	}
	store, err := a.store()
	if err != nil {
		return "", err
	}
	j, err := store.LoadJob(jobName)
	if errors.Is(err, state.ErrNotFound) {
		return "", exitcode.Usagef("no such job %q for backup id %q", jobName, arg)
	}
	if err != nil {
		return "", exitcode.With(exitcode.Failed, err)
	}
	dir := filepath.Join(j.DstParent(), name)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return "", exitcode.Usagef("backup %s does not exist (looked in %s)", arg, j.DstParent())
	}
	return dir, nil
}
