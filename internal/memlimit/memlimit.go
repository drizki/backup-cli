// Package memlimit gives the Go runtime a soft memory limit derived from the
// cgroup v2 limit, so the garbage collector works harder before the kernel
// kills the process for exceeding its container's memory.
package memlimit

import (
	"os"
	"strconv"
	"strings"
)

// CgroupFile is the cgroup v2 memory limit of the current cgroup namespace.
const CgroupFile = "/sys/fs/cgroup/memory.max"

// PeakPerFile is the measured peak resident memory of a run per file of the
// tree, in bytes, with the default garbage collector settings. See "Known
// limits" in docs/how-it-works.md for how it was measured.
const PeakPerFile = 1450

// Read returns the cgroup memory limit in bytes. ok is false when the file is
// missing, holds "max" (no limit) or cannot be parsed.
func Read(path string) (limit int64, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// Apply sets a soft limit of about 80% of the cgroup limit through set
// (debug.SetMemoryLimit) unless GOMEMLIMIT is already set. It returns the
// limit it applied, or 0 when it applied none.
func Apply(getenv func(string) string, path string, set func(int64) int64) int64 {
	if getenv("GOMEMLIMIT") != "" {
		return 0
	}
	limit, ok := Read(path)
	if !ok {
		return 0
	}
	soft := limit / 10 * 8
	set(soft)
	return soft
}
