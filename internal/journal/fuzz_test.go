package journal

import (
	"bytes"
	"strings"
	"testing"
)

func FuzzParse(f *testing.F) {
	sum := strings.Repeat("0", 64)
	f.Add([]byte(`{"t":"h","job":"a","run_id":"r","started_ns":5}` + "\n" + `{"t":"f","path":"x","size":1,"mtime_ns":2,"sha256":"` + sum + `"}` + "\n" + `{"t":"s"}` + "\n{\"t\":\"f\""))
	f.Add([]byte(`{"t":"h","job":"a"}` + "\n" + `{"t":"h","job":"b"}` + "\n"))
	f.Add([]byte(`{"t":"f","path":"","sha256":"short"}` + "\n\x00\xff\n"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		rep, err := parse(bytes.NewReader(raw))
		if err != nil {
			return
		}
		for p, e := range rep.Entries {
			if p == "" || len(e.SHA256) != 64 || e.Path != p {
				t.Fatalf("bad entry %q %+v", p, e)
			}
			_ = rep.Trusted(p) // must not panic
		}
	})
}
