package copyx

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrConflict means the destination holds an object of a different type than
// the one being created; nothing is removed to resolve it.
var ErrConflict = errors.New("destination holds an object of a different type")

func timeOf(ns int64) time.Time { return time.Unix(0, ns) }

// MakeDir creates a directory that stays writable for the owner until its
// final metadata is applied. An existing directory is reused.
func MakeDir(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return os.Mkdir(path, 0o700)
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s: %w", path, ErrConflict)
	}
	if fi.Mode().Perm()&0o700 == 0o700 {
		return nil
	}
	return os.Chmod(path, fi.Mode().Perm()|0o700)
}

// Symlink creates a symlink with the verbatim target and applies its
// ownership (when chown) and mtime. An existing identical link is kept; a
// different one is replaced atomically by renaming a new link over it.
func Symlink(path, target string, m Meta, chown bool) error {
	err := os.Symlink(target, path)
	if errors.Is(err, os.ErrExist) {
		err = replaceSymlink(path, target)
	}
	if err != nil {
		return err
	}
	if chown {
		if err := os.Lchown(path, int(m.UID), int(m.GID)); err != nil {
			return fmt.Errorf("chown %s: %w", path, err)
		}
	}
	return lutimes(path, m.MtimeNS)
}

func replaceSymlink(path, target string) error {
	cur, err := os.Readlink(path)
	if err == nil && cur == target {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, ErrConflict)
	}
	tmp := fmt.Sprintf("%s.link-%d", path, time.Now().UnixNano())
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
