package mount

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const table = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
23 22 0:20 / /mnt/backup rw - xfs /dev/sdb1 rw
24 23 0:21 / /mnt/backup/with\040space rw - zfs pool/ds rw
25 22 0:22 / /srv/data rw master:2 - btrfs /dev/sdc rw
short line
`

func writeTable(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadParsesPointsTypesAndEscapes(t *testing.T) {
	entries, err := Read(writeTable(t, table))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{"/", "ext4"}, {"/mnt/backup", "xfs"}, {"/mnt/backup/with space", "zfs"}, {"/srv/data", "btrfs"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("got %+v", entries)
	}
}

func TestReadMissingFile(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestInside(t *testing.T) {
	info := writeTable(t, table)
	got, err := Inside(info, "/mnt")
	if err != nil || !reflect.DeepEqual(got, []string{"/mnt/backup", "/mnt/backup/with space"}) {
		t.Fatalf("%v %v", got, err)
	}
	got, _ = Inside(info, "/srv/other")
	if len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if _, err := Inside(filepath.Join(t.TempDir(), "nope"), "/"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestFor(t *testing.T) {
	info := writeTable(t, table)
	e, ok, err := For(info, "/mnt/backup/job/x")
	if err != nil || !ok || e.Point != "/mnt/backup" || e.FSType != "xfs" {
		t.Fatalf("%+v %v %v", e, ok, err)
	}
	e, _, _ = For(info, "/home/x")
	if e.Point != "/" {
		t.Fatalf("%+v", e)
	}
	if _, ok, _ := For(writeTable(t, "short\n"), "/x"); ok {
		t.Fatal("empty table must not match")
	}
	if _, _, err := For(filepath.Join(t.TempDir(), "nope"), "/"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestUnescapeLeavesBadEscapes(t *testing.T) {
	for in, want := range map[string]string{`a\040b`: "a b", `a\zzzb`: `a\zzzb`, `end\04`: `end\04`, "plain": "plain"} {
		if got := unescape(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
