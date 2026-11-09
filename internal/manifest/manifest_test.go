package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var trickyNames = []string{
	"plain.txt", "with space.txt", "tab\there", "new\nline", `back\slash`, "pct%41", "ünï/cödé.txt", "dir/sub/file", "\x01ctl", "-dash", "end\r", "mid\rdle", "both\r\n",
}

func sampleEntries() []Entry {
	es := []Entry{{Path: RootPath, Type: TypeDir, Mode: 0o755, MtimeNS: 5}}
	for i, n := range trickyNames {
		es = append(es, Entry{Path: n, Type: TypeFile, Size: int64(i), MtimeNS: 1700000000123456789, Mode: 0o644, UID: 1000, GID: 100, SHA256: sum(n)})
	}
	es = append(es, Entry{Path: "link", Type: TypeSymlink, Mode: 0o777, LinkTarget: "../tar\tget\n"})
	return es
}

func TestEscapeRoundTrip(t *testing.T) {
	for _, s := range append(trickyNames, "", "%", "%%41", "\x7f") {
		got, err := Unescape(Escape(s))
		if err != nil || got != s {
			t.Errorf("%q -> %q (%v)", s, got, err)
		}
		if strings.ContainsAny(Escape(s), "\t\n") {
			t.Errorf("escaped %q still has tab or newline", s)
		}
	}
	for _, bad := range []string{"%", "%4", "%zz", "a%G1"} {
		if _, err := Unescape(bad); err == nil {
			t.Errorf("Unescape(%q) accepted", bad)
		}
	}
}

func TestTSVRoundTrip(t *testing.T) {
	in := sampleEntries()
	var buf bytes.Buffer
	if err := WriteTSV(&buf, in); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != len(in)+1 {
		t.Fatalf("one line per entry expected:\n%s", buf.String())
	}
	out, err := ParseTSV(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip differs:\n%v\n%v", in, out)
	}
}

func TestEachTSVStreamsEntriesAndStopsOnError(t *testing.T) {
	in := sampleEntries()
	var buf bytes.Buffer
	if err := WriteTSV(&buf, in); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	var got []Entry
	if err := EachTSV(strings.NewReader(raw), func(e Entry) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, got) {
		t.Fatalf("streamed entries differ:\n%v\n%v", in, got)
	}
	stop := errors.New("stop")
	n := 0
	err := EachTSV(strings.NewReader(raw), func(Entry) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("err %v after %d entries, want stop after 1", err, n)
	}
}

func TestParseTSVRejects(t *testing.T) {
	row := func(path, typ, size, mode, sha string) string {
		return tsvHeader + "\n" + strings.Join([]string{path, typ, size, "1", mode, "0", "0", sha, ""}, "\t") + "\n"
	}
	good := sum("x")
	cases := map[string]string{
		"empty":           "",
		"bad header":      "nope\n",
		"short row":       tsvHeader + "\na\tfile\n",
		"dotdot":          row("../x", "file", "1", "0644", good),
		"embedded dotdot": row("a/../x", "file", "1", "0644", good),
		"absolute":        row("/etc/passwd", "file", "1", "0644", good),
		"nul":             row("a%00b", "file", "1", "0644", good),
		"empty comp":      row("a//b", "file", "1", "0644", good),
		"dot comp":        row("a/./b", "file", "1", "0644", good),
		"bad type":        row("a", "fifo", "1", "0644", good),
		"bad size":        row("a", "file", "x", "0644", good),
		"negative size":   row("a", "file", "-1", "0644", good),
		"bad mode":        row("a", "file", "1", "9", good),
		"file w/o sha":    row("a", "file", "1", "0644", "-"),
		"bad escape":      row("a%zz", "file", "1", "0644", good),
	}
	for name, in := range cases {
		if _, err := ParseTSV(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestValidPath(t *testing.T) {
	for _, p := range []string{".", "a", "a/b", "dir/.hidden", "a b"} {
		if !ValidPath(p) {
			t.Errorf("%q rejected", p)
		}
	}
	for _, p := range []string{"", "/", "/a", "..", "a/..", "./a", "a/", "a\x00"} {
		if ValidPath(p) {
			t.Errorf("%q accepted", p)
		}
	}
}

// TestSHA256SumCheck proves MANIFEST.sha256 is accepted by the real tool,
// including names with spaces, newlines, backslashes and unicode.
func TestSHA256SumCheck(t *testing.T) {
	bin, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("sha256sum binary missing")
	}
	dir := t.TempDir()
	var entries []Entry
	for _, n := range trickyNames {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("data of "+n), 0o644); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, Entry{Path: n, Type: TypeFile, SHA256: sum("data of " + n)})
	}
	var buf bytes.Buffer
	if err := WriteSHA256(&buf, entries); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SHA256Name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func() ([]byte, error) {
		cmd := exec.Command(bin, "-c", "--quiet", SHA256Name)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}
	if out, err := check(); err != nil {
		t.Fatalf("sha256sum -c failed: %v\n%s\nmanifest:\n%s", err, out, buf.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := check(); err == nil {
		t.Fatal("sha256sum -c accepted a tampered file")
	}
}
