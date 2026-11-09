package backupset

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/job"
)

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func marker(t *testing.T, dir, jobName string, created time.Time) {
	t.Helper()
	if err := WriteMarker(dir, Marker{Job: jobName, RunID: "r", Created: created, FileCount: 3, Bytes: 9}); err != nil {
		t.Fatal(err)
	}
}

func testJob(parent string) job.Job {
	return job.Job{Name: "docs", Source: "/srv/data", DstTemplate: filepath.Join(parent, "docs-{timestamp}"), Keep: 2, Jobs: 4}
}

func TestScanClassifies(t *testing.T) {
	parent := t.TempDir()
	j := testJob(parent)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	newer := mkdir(t, filepath.Join(parent, "docs-20260102T000000Z"))
	marker(t, newer, "docs", t0.Add(24*time.Hour))
	older := mkdir(t, filepath.Join(parent, "docs-20260101T000000Z"))
	marker(t, older, "docs", t0)
	// .partial directory (even with a perfect marker inside)
	part := mkdir(t, filepath.Join(parent, "docs-20260103T000000Z.partial"))
	marker(t, part, "docs", t0.Add(48*time.Hour))
	// marker-less
	mkdir(t, filepath.Join(parent, "docs-20260104T000000Z"))
	// bad JSON marker
	bad := mkdir(t, filepath.Join(parent, "docs-20260105T000000Z"))
	if err := os.WriteFile(filepath.Join(bad, MarkerName), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	// marker for another job
	foreign := mkdir(t, filepath.Join(parent, "docs-20260106T000000Z"))
	marker(t, foreign, "someone-else", t0)
	// not matching the template: ignored entirely
	other := mkdir(t, filepath.Join(parent, "unrelated"))
	marker(t, other, "docs", t0)
	// regular file with a matching name
	if err := os.WriteFile(filepath.Join(parent, "docs-20260107T000000Z"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	all, err := Scan(j)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Status{}
	for _, b := range all {
		got[b.Name] = b.Status
	}
	want := map[string]Status{
		"docs-20260102T000000Z":         Committed,
		"docs-20260101T000000Z":         Committed,
		"docs-20260103T000000Z.partial": Partial,
		"docs-20260104T000000Z":         NoMarker,
		"docs-20260105T000000Z":         BadMarker,
		"docs-20260106T000000Z":         Foreign,
		"docs-20260107T000000Z":         NoMarker,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for n, s := range want {
		if got[n] != s {
			t.Errorf("%s: got %q want %q", n, got[n], s)
		}
	}

	checkOrderAndReasons(t, all)
}

func checkOrderAndReasons(t *testing.T, all []Backup) {
	t.Helper()
	com := OnlyCommitted(all)
	if len(com) != 2 || com[0].Name != "docs-20260101T000000Z" || com[1].Name != "docs-20260102T000000Z" {
		t.Fatalf("committed order: %+v", com)
	}
	if com[0].ID() != "docs/docs-20260101T000000Z" || !com[0].IsCommitted() || com[0].Marker.FileCount != 3 {
		t.Fatalf("backup: %+v", com[0])
	}
	for _, b := range all {
		if b.Status == BadMarker && b.Reason == "" {
			t.Error("bad marker needs a reason")
		}
	}
}

func TestReadMarkerValidation(t *testing.T) {
	d := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(d, MarkerName), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"format":2,"job":"a"}`)
	if _, err := ReadMarker(d); err == nil {
		t.Fatal("format 2 accepted")
	}
	write(`{"format":1}`)
	if _, err := ReadMarker(d); err == nil {
		t.Fatal("jobless marker accepted")
	}
	write(`{"format":1,"job":"a"}`)
	if m, err := ReadMarker(d); err != nil || m.Job != "a" {
		t.Fatal(err)
	}
	if _, err := ReadMarker(t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("got %v", err)
	}
}

func TestScanMissingParent(t *testing.T) {
	j := testJob(filepath.Join(t.TempDir(), "absent"))
	all, err := Scan(j)
	if err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o644)
	if _, err := Scan(testJob(f)); err == nil {
		t.Fatal("expected error when parent is a file")
	}
}
