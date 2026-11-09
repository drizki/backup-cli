package copyx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrNotRegular means the file to copy is not a regular file (it was replaced
// by a directory or another object after the tree was listed).
var ErrNotRegular = errors.New("not a regular file")

// FileOpts controls CopyFile.
type FileOpts struct {
	Buf []byte
	// Meta is applied to the copy. Nil means: use the source's own metadata
	// as seen before the copy (the right choice for live sources).
	Meta *Meta
	// AfterCopy runs after the data is copied and before the source is
	// stat'ed again. Tests use it to change the source mid-copy.
	AfterCopy func()
	// Chown restores uid/gid (root only).
	Chown bool
}

// FileResult describes a finished copy.
type FileResult struct {
	Size    int64
	SHA256  string
	Meta    Meta
	Changed bool // the source changed while it was being copied
}

// CopyFile copies the open file src to dstPath: single pass, hashing as it
// goes, fsync, then metadata. The destination is created or truncated and is
// never opened through a symlink. On error the partly written file is left
// in place; nothing is ever removed.
func CopyFile(ctx context.Context, dstPath string, src *os.File, o FileOpts) (FileResult, error) {
	before, err := src.Stat()
	if err != nil {
		return FileResult{}, err
	}
	if !before.Mode().IsRegular() {
		return FileResult{}, ErrNotRegular
	}
	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|noFollow, 0o600)
	if err != nil {
		return FileResult{}, err
	}
	res, err := copyOpen(ctx, dst, src, before, o)
	if cerr := dst.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return res, fmt.Errorf("copy to %s: %w", dstPath, err)
	}
	if o.Meta != nil {
		res.Meta = *o.Meta
	}
	return res, Apply(dstPath, res.Meta, o.Chown)
}

func copyOpen(ctx context.Context, dst, src *os.File, before os.FileInfo, o FileOpts) (FileResult, error) {
	res := FileResult{Meta: MetaOf(before)}
	var err error
	res.Size, res.SHA256, err = Copy(ctx, dst, src, o.Buf)
	if err != nil {
		return res, err
	}
	if o.AfterCopy != nil {
		o.AfterCopy()
	}
	after, err := src.Stat()
	if err != nil {
		return res, err
	}
	res.Changed = !sameFile(before, after) || res.Size != before.Size()
	return res, dst.Sync()
}

func sameFile(a, b os.FileInfo) bool {
	if a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	sa, oka := a.Sys().(*syscall.Stat_t)
	sb, okb := b.Sys().(*syscall.Stat_t)
	return !oka || !okb || sa.Ctim == sb.Ctim
}
