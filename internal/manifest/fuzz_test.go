package manifest

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func FuzzParseTSV(f *testing.F) {
	sum := strings.Repeat("a", 64)
	f.Add([]byte(tsvHeader + "\n.\tdir\t0\t1\t0755\t0\t0\t-\t\na%09b\tfile\t3\t5\t0644\t1\t1\t" + sum + "\t\n"))
	f.Add([]byte(tsvHeader + "\nl\tsymlink\t0\t5\t0777\t0\t0\t-\t..%2Ftarget%0A\n"))
	f.Add([]byte(tsvHeader + "\n../x\tfile\t0\t0\t0\t0\t0\t" + strings.Repeat("0", 64) + "\t\n"))
	f.Add([]byte(tsvHeader + "\n/abs\tfile\t0\t0\t0\t0\t0\t" + strings.Repeat("0", 64) + "\t\n"))
	f.Add([]byte("not a manifest"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		es, err := ParseTSV(bytes.NewReader(raw))
		if err != nil {
			return
		}
		for _, e := range es {
			if !ValidPath(e.Path) {
				t.Fatalf("accepted unsafe path %q", e.Path)
			}
		}
		var buf bytes.Buffer
		if err := WriteTSV(&buf, es); err != nil {
			t.Fatal(err)
		}
		again, err := ParseTSV(&buf)
		if err != nil {
			t.Fatalf("re-parse: %v\n%q", err, buf.String())
		}
		if len(es) > 0 && !reflect.DeepEqual(es, again) {
			t.Fatalf("round trip differs:\n%#v\n%#v", es, again)
		}
	})
}

// gnuName undoes the GNU coreutils escaping that sha256sum -c applies to a
// manifest line, returning the file name the tool would open.
func gnuName(line string) (string, bool) {
	escaped := strings.HasPrefix(line, "\\")
	line = strings.TrimPrefix(line, "\\")
	if len(line) < 67 || line[64:66] != "  " {
		return "", false
	}
	name := line[66:]
	if !escaped {
		return name, true
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] != '\\' {
			b.WriteByte(name[i])
			continue
		}
		i++
		if i == len(name) {
			return "", false
		}
		switch name[i] {
		case '\\':
			b.WriteByte('\\')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		default:
			return "", false
		}
	}
	return b.String(), true
}

func FuzzSHA256Line(f *testing.F) {
	for _, s := range []string{"plain", "a\nb\\c", "Icon\r", "mid\rdle", "back\\", "\\n", "tab\there", "ünï/cödé"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if !ValidPath(p) || p == RootPath {
			return
		}
		line := sha256Line(strings.Repeat("ab", 32), p)
		body := strings.TrimSuffix(line, "\n")
		if strings.ContainsAny(body, "\n\r") {
			t.Fatalf("%q: the line has a raw line break: %q", p, line)
		}
		name, ok := gnuName(body)
		if !ok || name != "./"+p {
			t.Fatalf("%q round-tripped to %q (%v) via %q", p, name, ok, line)
		}
	})
}
