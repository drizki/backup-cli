// Package manifest writes, parses and verifies the two manifests stored in
// every backup: MANIFEST.sha256 (accepted by "sha256sum -c") and MANIFEST.tsv
// (the full tree description used to seed the next run).
package manifest

import (
	"bufio"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// Manifest file names inside a backup.
const (
	SHA256Name = "MANIFEST.sha256"
	TSVName    = "MANIFEST.tsv"
)

// Entry types.
const (
	TypeFile    = "file"
	TypeDir     = "dir"
	TypeSymlink = "symlink"
)

// RootPath is the manifest path of the backup's top directory.
const RootPath = "."

// Entry describes one object of a backup tree.
type Entry struct {
	Path       string // slash-separated, relative, "." for the root
	Type       string
	Size       int64
	MtimeNS    int64
	Mode       uint32 // permission and setuid/setgid/sticky bits
	UID, GID   uint32
	SHA256     string // hex; empty for directories and symlinks
	LinkTarget string
}

const tsvHeader = "path\ttype\tsize\tmtime_ns\tmode\tuid\tgid\tsha256\tlink_target"

// ValidPath reports whether p is a safe manifest path: relative, clean, no
// NUL, no "." or ".." components (except the lone root ".").
func ValidPath(p string) bool {
	if p == RootPath {
		return true
	}
	if p == "" || strings.ContainsRune(p, 0) || strings.HasPrefix(p, "/") {
		return false
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." {
			return false
		}
	}
	return path.Clean(p) == p
}

// Escape percent-encodes %, control characters and DEL so a value never
// contains a tab or newline.
func Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c < 0x20 || c == 0x7f {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Unescape reverses Escape.
func Unescape(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+3 > len(s) {
			return "", fmt.Errorf("truncated escape in %q", s)
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return "", fmt.Errorf("bad escape in %q", s)
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), nil
}

// sha256Line formats one manifest line using GNU coreutils escaping: a name
// containing a backslash, newline or carriage return gets a leading backslash
// on the line and those characters are written as \\, \n and \r.
func sha256Line(hexSum, rel string) string {
	name := "./" + rel
	if !strings.ContainsAny(name, "\\\n\r") {
		return hexSum + "  " + name + "\n"
	}
	name = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r").Replace(name)
	return "\\" + hexSum + "  " + name + "\n"
}

// WriteSHA256 writes the sha256sum-compatible manifest for the file entries.
func WriteSHA256(w io.Writer, entries []Entry) error {
	bw := bufio.NewWriter(w)
	for _, e := range entries {
		if e.Type != TypeFile {
			continue
		}
		if _, err := bw.WriteString(sha256Line(e.SHA256, e.Path)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// WriteTSV writes the tab-separated manifest with a header row.
func WriteTSV(w io.Writer, entries []Entry) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString(tsvHeader + "\n"); err != nil {
		return err
	}
	for _, e := range entries {
		sum := e.SHA256
		if sum == "" {
			sum = "-"
		}
		_, err := fmt.Fprintf(bw, "%s\t%s\t%d\t%d\t%04o\t%d\t%d\t%s\t%s\n",
			Escape(e.Path), e.Type, e.Size, e.MtimeNS, e.Mode, e.UID, e.GID, sum, Escape(e.LinkTarget))
		if err != nil {
			return err
		}
	}
	return bw.Flush()
}
