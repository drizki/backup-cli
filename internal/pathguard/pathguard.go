// Package pathguard provides realpath-aware containment and overlap checks.
package pathguard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns an absolute, symlink-free form of p. When p (or trailing
// components of it) does not exist yet, the nearest existing ancestor is
// resolved and the remaining components are appended verbatim.
func Resolve(p string) (string, error) {
	if !filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		p = abs
	}
	p = filepath.Clean(p)
	var rest []string
	cur := p
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve %s: %w", cur, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("resolve %s: no existing ancestor", p)
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// Contains reports whether child equals parent or lies inside it. Both must
// already be cleaned absolute paths (use Resolve first).
func Contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// Overlaps reports whether a and b are the same location or one is inside the
// other, after resolving symlinks. The paths need not exist.
func Overlaps(a, b string) (bool, error) {
	ra, err := Resolve(a)
	if err != nil {
		return false, err
	}
	rb, err := Resolve(b)
	if err != nil {
		return false, err
	}
	return Contains(ra, rb) || Contains(rb, ra), nil
}
