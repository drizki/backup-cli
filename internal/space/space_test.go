package space

import (
	"errors"
	"strings"
	"testing"
)

func TestRequired(t *testing.T) {
	if got := Required(0, 0); got != Headroom {
		t.Errorf("empty = %d", got)
	}
	if got := Required(1000, 2000); got != 2000+100+Headroom {
		t.Errorf("larger wins: %d", got)
	}
	if got := Required(5000, 10); got != 5000+250+Headroom {
		t.Errorf("previous backup wins: %d", got)
	}
	if got := Required(-5, -9); got != Headroom {
		t.Errorf("negative sizes: %d", got)
	}
}

func TestShortError(t *testing.T) {
	err := error(&ShortError{Need: 3 << 30, Have: 1 << 20})
	var se *ShortError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "need 3.0 GiB, have 1.0 MiB") {
		t.Fatalf("%v", err)
	}
}

func TestFreeReportsRealFilesystem(t *testing.T) {
	n, err := Free(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := Free("/nonexistent-path-for-test"); err == nil {
		t.Fatal("missing path accepted")
	}
}

func TestFormat(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB", 3 << 40: "3.0 TiB"}
	for n, want := range cases {
		if got := Format(n); got != want {
			t.Errorf("Format(%d) = %q, want %q", n, got, want)
		}
	}
}
