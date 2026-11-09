// Package space estimates the room a backup needs and checks the free space
// of the destination filesystem.
package space

import (
	"fmt"
	"syscall"
)

// Headroom is added to every estimate.
const Headroom = 64 << 20

// StatFunc reports the bytes available to unprivileged writers at path.
type StatFunc func(path string) (uint64, error)

// Free is the production StatFunc, based on statfs(2).
func Free(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// Required estimates the space for a full copy: the larger of the previous
// backup and the current source, plus 5% and the fixed headroom.
func Required(lastBytes, sourceBytes int64) uint64 {
	n := max(lastBytes, sourceBytes, 0)
	return uint64(n) + uint64(n)/20 + Headroom
}

// ShortError is returned by Check when there is not enough room.
type ShortError struct{ Need, Have uint64 }

func (e *ShortError) Error() string {
	return fmt.Sprintf("not enough free space: need %s, have %s", Format(e.Need), Format(e.Have))
}

// Format renders a byte count for messages.
func Format(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
