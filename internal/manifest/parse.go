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
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	if !sc.Scan() {
		return nil, fmt.Errorf("manifest is empty")
	}
	if sc.Text() != tsvHeader {
		return nil, fmt.Errorf("manifest header is not recognised")
	}
	var out []Entry
	for n := 2; sc.Scan(); n++ {
		e, err := parseRow(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("manifest line %d: %w", n, err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
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

// SumLine is one parsed MANIFEST.sha256 line.
type SumLine struct {
	SHA256 string
	Path   string // without the leading "./"
}

// ParseSHA256 reads a MANIFEST.sha256 written by WriteSHA256 (GNU format).
func ParseSHA256(r io.Reader) ([]SumLine, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out []SumLine
	for n := 1; sc.Scan(); n++ {
		l, err := parseSumLine(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, l)
	}
	return out, sc.Err()
}

func parseSumLine(line string) (SumLine, error) {
	escaped := strings.HasPrefix(line, "\\")
	line = strings.TrimPrefix(line, "\\")
	if len(line) < 67 || line[64:66] != "  " || !strings.HasPrefix(line[66:], "./") {
		return SumLine{}, fmt.Errorf("malformed line")
	}
	name := line[68:]
	if escaped {
		name = strings.NewReplacer(`\\`, `\`, `\n`, "\n").Replace(name)
	}
	if !ValidPath(name) {
		return SumLine{}, fmt.Errorf("unsafe path %q", name)
	}
	return SumLine{SHA256: line[:64], Path: name}, nil
}
