// Package mount reads a mountinfo table (normally /proc/self/mountinfo) and
// answers mount point questions. The table path is injectable for tests.
package mount

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultInfo is the kernel's view of the calling process's mounts.
const DefaultInfo = "/proc/self/mountinfo"

// Entry is one mount.
type Entry struct {
	Point  string
	FSType string
}

// Read parses the mountinfo file at path.
func Read(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		e, ok := parseLine(sc.Text())
		if ok {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// parseLine extracts the mount point (field 5) and the filesystem type (the
// field after the "-" separator) from one mountinfo line.
func parseLine(line string) (Entry, bool) {
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return Entry{}, false
	}
	e := Entry{Point: filepath.Clean(unescape(fields[4]))}
	for i := 6; i < len(fields)-1; i++ {
		if fields[i] == "-" {
			e.FSType = fields[i+1]
			break
		}
	}
	return e, true
}

// unescape decodes the octal escapes (\040 for a space) the kernel uses.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Inside lists the mount points at or below dir.
func Inside(info, dir string) ([]string, error) {
	entries, err := Read(info)
	if err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	var out []string
	for _, e := range entries {
		if within(dir, e.Point) {
			out = append(out, e.Point)
		}
	}
	return out, nil
}

// For returns the mount holding path p: the entry with the longest mount
// point that contains p. ok is false when the table has no such entry.
func For(info, p string) (Entry, bool, error) {
	entries, err := Read(info)
	if err != nil {
		return Entry{}, false, err
	}
	p = filepath.Clean(p)
	var best Entry
	found := false
	for _, e := range entries {
		if within(e.Point, p) && (!found || len(e.Point) >= len(best.Point)) {
			best, found = e, true
		}
	}
	return best, found, nil
}

// within reports whether child equals parent or lies below it.
func within(parent, child string) bool {
	if parent == child || parent == "/" {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}
