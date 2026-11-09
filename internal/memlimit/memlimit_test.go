package memlimit

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "memory.max")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadParsesALimitAndIgnoresTheRest(t *testing.T) {
	if v, ok := Read(write(t, "2147483648\n")); !ok || v != 2<<30 {
		t.Fatalf("got %d %v", v, ok)
	}
	for _, in := range []string{"max\n", "", "abc", "0", "-5"} {
		if v, ok := Read(write(t, in)); ok {
			t.Errorf("%q: got limit %d", in, v)
		}
	}
	if _, ok := Read(filepath.Join(t.TempDir(), "absent")); ok {
		t.Error("a missing file reported a limit")
	}
}

func TestApplySetsEightyPercentUnlessGOMEMLIMITIsSet(t *testing.T) {
	var got int64
	set := func(n int64) int64 { got = n; return 0 }
	none := func(string) string { return "" }
	if n := Apply(none, write(t, "1000000\n"), set); n != 800000 || got != 800000 {
		t.Fatalf("applied %d, set %d", n, got)
	}
	got = 0
	if n := Apply(func(string) string { return "450MiB" }, write(t, "1000000"), set); n != 0 || got != 0 {
		t.Fatalf("GOMEMLIMIT ignored: %d %d", n, got)
	}
	if n := Apply(none, write(t, "max"), set); n != 0 || got != 0 {
		t.Fatalf("an unlimited cgroup set a limit: %d %d", n, got)
	}
}
