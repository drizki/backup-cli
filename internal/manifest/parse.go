package manifest

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ParseTSV reads a MANIFEST.tsv. Every path is validated so a poisoned
// manifest cannot name anything outside the backup directory.
func ParseTSV(r io.Reader) ([]Entry, error) {
	var out []Entry
	err := EachTSV(r, func(e Entry) error {
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// EachTSV parses a MANIFEST.tsv like ParseTSV but hands each entry to fn
// instead of collecting them, so a caller can keep a compact form of a very
// large manifest. It stops at the first error from fn. The strings of an
// entry may share memory with the line they came from; clone what you keep.
func EachTSV(r io.Reader, fn func(Entry) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if !sc.Scan() {
		return fmt.Errorf("manifest is empty")
	}
	if sc.Text() != tsvHeader {
		return fmt.Errorf("manifest header is not recognised")
	}
	for n := 2; sc.Scan(); n++ {
		e, err := parseRow(sc.Text())
		if err != nil {
			return fmt.Errorf("manifest line %d: %w", n, err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return sc.Err()
}

func parseRow(line string) (Entry, error) {
	f := strings.Split(line, "\t")
	if len(f) != 9 {
		return Entry{}, fmt.Errorf("expected 9 fields, got %d", len(f))
	}
	var e Entry
	if err := parseHead(f, &e); err != nil {
		return e, err
	}
	if err := parseNumbers(f, &e); err != nil {
		return e, err
	}
	if f[7] != "-" {
		e.SHA256 = f[7]
	}
	if e.Type == TypeFile && len(e.SHA256) != 64 {
		return e, fmt.Errorf("file %q has no sha256", e.Path)
	}
	var err error
	e.LinkTarget, err = Unescape(f[8])
	return e, err
}

func parseHead(f []string, e *Entry) error {
	var err error
	if e.Path, err = Unescape(f[0]); err != nil {
		return err
	}
	if !ValidPath(e.Path) {
		return fmt.Errorf("unsafe path %q", e.Path)
	}
	e.Type = f[1]
	if e.Type != TypeFile && e.Type != TypeDir && e.Type != TypeSymlink {
		return fmt.Errorf("unknown type %q", e.Type)
	}
	return nil
}

func parseNumbers(f []string, e *Entry) error {
	var err error
	if e.Size, err = strconv.ParseInt(f[2], 10, 64); err != nil || e.Size < 0 {
		return fmt.Errorf("bad size %q", f[2])
	}
	if e.MtimeNS, err = strconv.ParseInt(f[3], 10, 64); err != nil {
		return fmt.Errorf("bad mtime %q", f[3])
	}
	if e.Mode, err = parseUint(f[4], 8, "mode"); err != nil {
		return err
	}
	if e.UID, err = parseUint(f[5], 10, "uid"); err != nil {
		return err
	}
	e.GID, err = parseUint(f[6], 10, "gid")
	return err
}

func parseUint(s string, base int, what string) (uint32, error) {
	v, err := strconv.ParseUint(s, base, 32)
	if err != nil {
		return 0, fmt.Errorf("bad %s %q", what, s)
	}
	return uint32(v), nil
}
