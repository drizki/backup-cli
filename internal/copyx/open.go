package copyx

import (
	"errors"
	"os"
	"syscall"
)

const noFollow = syscall.O_NOFOLLOW

// FlagOpener opens a file with the given flags.
type FlagOpener func(flags int) (*os.File, error)

// OpenReadOnly opens read-only without following a final symlink, asking the
// kernel not to update the access time. When the kernel refuses O_NOATIME
// (EPERM, for files owned by someone else) it retries without it.
func OpenReadOnly(open FlagOpener) (*os.File, error) {
	base := os.O_RDONLY | noFollow
	f, err := open(base | syscall.O_NOATIME)
	if errors.Is(err, syscall.EPERM) {
		return open(base)
	}
	return f, err
}

// RootOpener adapts a rooted open (which cannot escape its root) for a
// fixed relative path.
func RootOpener(root *os.Root, rel string) FlagOpener {
	return func(flags int) (*os.File, error) { return root.OpenFile(rel, flags, 0) }
}
