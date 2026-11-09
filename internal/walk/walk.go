// Package walk lists a source tree using metadata only. It never opens a
// regular file and never follows a symlink.
package walk

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

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
	Files   int64
	Bytes   int64 // sum of regular file sizes
}

// Walk lists root. Any unreadable directory or entry is an error: a backup
// that silently omits data is worse than a failed one.
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
	items, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		fi, err := it.Info()
		if err != nil {
			return err
		}
		childRel := path.Join(rel, it.Name())
		childAbs := filepath.Join(abs, it.Name())
		if fi.IsDir() {
			res.Entries = append(res.Entries, entryOf(childRel, manifest.TypeDir, fi))
			if err := walkDir(ctx, childAbs, childRel, res); err != nil {
				return err
			}
			continue
		}
		if err := res.add(childAbs, childRel, fi); err != nil {
			return err
		}
	}
	return nil
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
