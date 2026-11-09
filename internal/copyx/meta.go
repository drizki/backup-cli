package copyx

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// Meta is the metadata applied to copied objects.
type Meta struct {
	Mode     uint32 // permission plus setuid/setgid/sticky bits
	UID, GID uint32
	MtimeNS  int64
}

// MetaOf extracts Meta from file info obtained from lstat or fstat.
func MetaOf(fi os.FileInfo) Meta {
	m := Meta{MtimeNS: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		m.Mode = st.Mode & 0o7777
		m.UID, m.GID = st.Uid, st.Gid
	}
	return m
}

// IsRoot reports whether ownership can be restored.
func IsRoot() bool { return os.Geteuid() == 0 }

// Apply sets ownership (only when chown is true), mode and mtime on a
// non-symlink path. Ownership goes first because chown clears setuid bits.
func Apply(path string, m Meta, chown bool) error {
	if chown {
		if err := os.Lchown(path, int(m.UID), int(m.GID)); err != nil {
			return fmt.Errorf("chown %s: %w", path, err)
		}
	}
	if err := syscall.Chmod(path, m.Mode); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := os.Chtimes(path, timeOf(m.MtimeNS), timeOf(m.MtimeNS)); err != nil {
		return fmt.Errorf("set mtime %s: %w", path, err)
	}
	return nil
}

// lutimes sets the access and modification time of a symlink itself.
func lutimes(path string, ns int64) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	ts := [2]syscall.Timespec{syscall.NsecToTimespec(ns), syscall.NsecToTimespec(ns)}
	dirfd := -100 // AT_FDCWD
	const atSymlinkNoFollow = 0x100
	_, _, e := syscall.Syscall6(syscall.SYS_UTIMENSAT, uintptr(dirfd), uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&ts[0])), atSymlinkNoFollow, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}
