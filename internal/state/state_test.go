package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/job"
)

func sampleJob(name string) job.Job {
	return job.Job{Version: 1, Name: name, Source: "/srv/data", DstTemplate: "/mnt/backup/" + name + "-{timestamp}",
		Schedule: "daily,02:00", OnCalendar: "*-*-* 02:00:00", Keep: 3, Jobs: 4, VerifySamplePct: 2,
		Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func TestJobRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	j := sampleJob("docs")
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadJob("docs")
	if err != nil || got != j {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	fi, err := os.Stat(s.JobPath("docs"))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode: %v %v", fi, err)
	}
	// Plain JSON with the documented keys, readable with jq.
	b, _ := os.ReadFile(s.JobPath("docs"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"version", "name", "source", "dst_template", "schedule", "on_calendar", "biweekly", "keep",
		"deadline", "on_success", "on_failure", "scheduled", "created", "verify_sample_pct"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	// No leftover temp files.
	ents, _ := os.ReadDir(s.JobsDir())
	if len(ents) != 1 {
		t.Fatalf("leftovers: %v", ents)
	}
}

func TestSaveJobRejectsInvalid(t *testing.T) {
	s := New(t.TempDir())
	j := sampleJob("docs")
	j.Keep = 0
	if err := s.SaveJob(j); err == nil {
		t.Fatal("expected validation error")
	}
	if _, err := os.Stat(s.JobPath("docs")); err == nil {
		t.Fatal("invalid job written")
	}
}

func TestLoadJobErrors(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.LoadJob("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.LoadJob("../etc"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("traversal name accepted: %v", err)
	}
	_ = s.Ensure()
	_ = os.WriteFile(s.JobPath("bad"), []byte("{not json"), 0o644)
	if _, err := s.LoadJob("bad"); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("got %v", err)
	}
}

func TestListJobsSortedAndTolerant(t *testing.T) {
	s := New(t.TempDir())
	if jobs, errs := s.ListJobs(); jobs != nil || errs != nil {
		t.Fatal("missing dir should be empty")
	}
	_ = s.Ensure()
	for _, n := range []string{"zeta", "alpha"} {
		if err := s.SaveJob(sampleJob(n)); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(s.JobPath("broken"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(s.JobsDir(), "Not Valid.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(s.JobsDir(), "readme.txt"), []byte("x"), 0o644)
	jobs, errs := s.ListJobs()
	if len(jobs) != 2 || jobs[0].Name != "alpha" || jobs[1].Name != "zeta" {
		t.Fatalf("jobs: %+v", jobs)
	}
	if len(errs) != 1 {
		t.Fatalf("errs: %v", errs)
	}
}

func TestRemoveJob(t *testing.T) {
	s := New(t.TempDir())
	_ = s.Ensure()
	_ = s.SaveJob(sampleJob("docs"))
	if err := s.RemoveJob("docs"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveJob("docs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.RemoveJob("../x"); err == nil {
		t.Fatal("traversal accepted")
	}
}

func TestStatusAndRuns(t *testing.T) {
	s := New(t.TempDir())
	t0 := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	id1 := NewRunID(t0, "docs")
	id2 := NewRunID(t0.Add(time.Hour), "docs")
	id3 := NewRunID(t0.Add(2*time.Hour), "media")
	re := regexp.MustCompile(`^20260203T040506Z-docs-[0-9a-f]{4}$`)
	if !re.MatchString(id1) {
		t.Fatalf("run id %q", id1)
	}
	for i, id := range []string{id1, id2, id3} {
		st := Status{RunID: id, Job: strings.Split(id, "-")[1], State: StateRunning, Started: t0.Add(time.Duration(i) * time.Hour), Log: s.RunDir(id) + "/run.log"}
		if err := s.WriteStatus(st); err != nil {
			t.Fatal(err)
		}
	}
	st, err := s.ReadStatus(id1)
	if err != nil || st.Terminal() || st.Job != "docs" {
		t.Fatalf("%+v %v", st, err)
	}
	st.State = StateSucceeded
	st.Finished = t0.Add(time.Minute)
	if err := s.WriteStatus(st); err != nil {
		t.Fatal(err)
	}
	st, _ = s.ReadStatus(id1)
	if !st.Terminal() || st.State != "succeeded" {
		t.Fatalf("%+v", st)
	}
	all, errs := s.ListRuns("")
	if len(errs) != 0 || len(all) != 3 || all[0].RunID != id3 {
		t.Fatalf("all: %+v %v", all, errs)
	}
	only, _ := s.ListRuns("docs")
	if len(only) != 2 {
		t.Fatalf("filtered: %d", len(only))
	}
	// Unfinished run: no "finished" key.
	b, _ := os.ReadFile(s.StatusPath(id2))
	if strings.Contains(string(b), `"finished"`) {
		t.Fatal("finished should be omitted while running")
	}
}

func TestStatusErrors(t *testing.T) {
	s := New(t.TempDir())
	if err := s.WriteStatus(Status{}); err == nil {
		t.Fatal("empty run id accepted")
	}
	if _, err := s.ReadStatus("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "..", "a/b"} {
		if _, err := s.ReadStatus(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	_ = os.MkdirAll(s.RunDir("corrupt"), 0o755)
	_ = os.WriteFile(s.StatusPath("corrupt"), []byte("{"), 0o644)
	_ = os.MkdirAll(s.RunDir("empty"), 0o755)
	runs, errs := s.ListRuns("")
	if len(runs) != 0 || len(errs) != 1 {
		t.Fatalf("%v %v", runs, errs)
	}
	if runs, errs := New(filepath.Join(t.TempDir(), "none")).ListRuns(""); runs != nil || errs != nil {
		t.Fatal("missing runs dir should be empty")
	}
}

func TestWriteFileAtomicFailureLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	_ = os.MkdirAll(target, 0o755) // rename of a file onto a directory fails
	if err := WriteFileAtomic(target, []byte("x"), 0o644); err == nil {
		t.Fatal("expected rename failure")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp file leaked: %v", ents)
	}
	if err := WriteFileAtomic(filepath.Join(dir, "nodir", "f"), nil, 0o644); err == nil {
		t.Fatal("expected open failure")
	}
	if err := SyncDir(filepath.Join(dir, "absent")); err == nil {
		t.Fatal("expected sync error")
	}
}

func TestEnsure(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "a", "b"))
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{s.JobsDir(), s.RunsDir(), s.LocksDir()} {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o755 {
			t.Fatalf("%s: %v %v", d, fi, err)
		}
	}
	if s.LockPath("x") != filepath.Join(s.Dir, "locks", "x.lock") {
		t.Fatal("lock path")
	}
	f := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(f, nil, 0o644)
	if err := New(filepath.Join(f, "sub")).Ensure(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadJobCleansTheTemplate(t *testing.T) {
	s := New(t.TempDir())
	j := sampleJob("docs")
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.JobPath("docs"))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "/mnt/backup/", "/mnt//backup/./", 1)
	if err := os.WriteFile(s.JobPath("docs"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadJob("docs")
	if err != nil || got.DstTemplate != j.DstTemplate {
		t.Fatalf("template %q, %v", got.DstTemplate, err)
	}
}
