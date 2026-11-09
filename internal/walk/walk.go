// Package walk lists a source tree using metadata only. It never opens a
// regular file and never follows a symlink.
package walk

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	"github.com/drizki/backup-cli/internal/copyx"
	"github.com/drizki/backup-cli/internal/manifest"
)

// Skipped is a special file (fifo, socket, device) that is not backed up.
type Skipped struct {
	Path string
	Kind string
}

// Result is a walked tree: entries in a stable parent-before-child order,
// beginning with the root directory entry ".".
type Result struct {
	Entries []manifest.Entry
	Skipped []Skipped
	// Vanished lists paths that disappeared or changed type while the tree
	// was being walked; they are left out of Entries.
	Vanished []string
	Files    int64
	Bytes    int64 // sum of regular file sizes

	beforeList func(dir string) // tests change the tree just before a directory is listed
}

// Walk lists root. Any unreadable directory or entry is an error: a backup
// that silently omits data is worse than a failed one. An entry that vanishes
// during the walk (deleted, or replaced by an object of another type) is not
// an error; it is reported in Result.Vanished.
func Walk(ctx context.Context, root string) (Result, error) {
	var res Result
	fi, err := os.Stat(root)
	if err != nil {
		return res, err
	}
	if !fi.IsDir() {
		return res, fmt.Errorf("%s is not a directory", root)
	}
	res.Entries = append(res.Entries, entryOf(manifest.RootPath, manifest.TypeDir, fi))
	return res, walkDir(ctx, root, "", &res)
}

func walkDir(ctx context.Context, abs, rel string, res *Result) error {
	if res.beforeList != nil {
		res.beforeList(abs)
	}
	items, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		childRel := path.Join(rel, it.Name())
		if err := res.visit(ctx, filepath.Join(abs, it.Name()), childRel, it); err != nil {
			if !Vanished(err) {
				return err
			}
			res.Vanished = append(res.Vanished, childRel)
		}
	}
	return nil
}

// visit adds one directory entry (and, for a directory, its subtree). When a
// directory vanishes before it can be listed, its entry is taken back and the
// error returned.
func (res *Result) visit(ctx context.Context, abs, rel string, it fs.DirEntry) error {
	fi, err := it.Info()
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return res.add(abs, rel, fi)
	}
	res.Entries = append(res.Entries, entryOf(rel, manifest.TypeDir, fi))
	err = walkDir(ctx, abs, rel, res)
	if Vanished(err) { // only the directory's own listing can fail this way: nothing below it was added
		res.Entries = res.Entries[:len(res.Entries)-1]
	}
	return err
}

// Vanished reports whether err means the object disappeared or changed type
// (deleted, or replaced by a file where a directory was expected).
func Vanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EISDIR)
}

func (res *Result) add(abs, rel string, fi fs.FileInfo) error {
	switch {
	case fi.Mode().IsRegular():
		res.Entries = append(res.Entries, entryOf(rel, manifest.TypeFile, fi))
		res.Files++
		res.Bytes += fi.Size()
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			return err
		}
		e := entryOf(rel, manifest.TypeSymlink, fi)
		e.LinkTarget = target
		res.Entries = append(res.Entries, e)
	default:
		res.Skipped = append(res.Skipped, Skipped{Path: rel, Kind: kindOf(fi.Mode())})
	}
	return nil
}

func entryOf(rel, typ string, fi fs.FileInfo) manifest.Entry {
	m := copyx.MetaOf(fi)
	e := manifest.Entry{Path: rel, Type: typ, MtimeNS: m.MtimeNS, Mode: m.Mode, UID: m.UID, GID: m.GID}
	if typ == manifest.TypeFile {
		e.Size = fi.Size()
	}
	return e
}

func kindOf(m fs.FileMode) string {
	switch {
	case m&fs.ModeNamedPipe != 0:
		return "fifo"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m&fs.ModeCharDevice != 0:
		return "character device"
	case m&fs.ModeDevice != 0:
		return "block device"
	}
	return "special file"
}
