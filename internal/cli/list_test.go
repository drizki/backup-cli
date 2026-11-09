package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/state"
)

func TestListJobs(t *testing.T) {
	e := newEnv(t)
	e.mustRun("list", "jobs")
	if strings.TrimSpace(e.out.String()) != "no jobs" {
		t.Fatal(e.out.String())
	}
	e.mustRun("list", "jobs", "--json")
	if strings.TrimSpace(e.out.String()) != "[]" {
		t.Fatalf("empty json must be []: %q", e.out.String())
	}
	e.mustRun("add", e.src, e.tmpl("one"), "--name", "one", "--schedule", "daily,01:00")
	e.mustRun("add", e.src, e.tmpl("two"), "--name", "two", "--no-schedule")
	e.mustRun("list", "jobs")
	out := e.out.String()
	for _, want := range []string{"NAME", "one", "two", "daily,01:00", "true", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
	e.mustRun("list", "jobs", "--json")
	var js []map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &js); err != nil || len(js) != 2 || js[0]["name"] != "one" {
		t.Fatalf("json: %v %s", err, e.out.String())
	}
	// A corrupt job file is a warning, not a failure.
	if err := os.WriteFile(e.store().JobPath("broken"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun("list", "jobs")
	if !strings.Contains(e.errb.String(), "warning") {
		t.Fatal("no warning for corrupt job")
	}
}

func TestListRuns(t *testing.T) {
	e := newEnv(t)
	e.mustRun("list", "runs")
	if strings.TrimSpace(e.out.String()) != "no runs" {
		t.Fatal(e.out.String())
	}
	e.mustRun("list", "runs", "--json")
	if strings.TrimSpace(e.out.String()) != "[]" {
		t.Fatal(e.out.String())
	}
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := e.store()
	for i, jobName := range []string{"a", "b"} {
		st := state.Status{RunID: state.NewRunID(t0.Add(time.Duration(i)*time.Hour), jobName), Job: jobName,
			State: state.StateSucceeded, Started: t0, Finished: t0.Add(90 * time.Second)}
		if err := s.WriteStatus(st); err != nil {
			t.Fatal(err)
		}
	}
	running := state.Status{RunID: state.NewRunID(t0.Add(5*time.Hour), "a"), Job: "a", State: state.StateRunning, Started: t0}
	if err := s.WriteStatus(running); err != nil {
		t.Fatal(err)
	}
	e.mustRun("list", "runs")
	out := e.out.String()
	if !strings.Contains(out, "1m30s") || !strings.Contains(out, "running") || strings.Index(out, running.RunID) > strings.Index(out, "-b-") {
		t.Fatalf("table:\n%s", out)
	}
	e.mustRun("list", "runs", "--job", "b", "--json")
	var rs []state.Status
	if err := json.Unmarshal(e.out.Bytes(), &rs); err != nil || len(rs) != 1 || rs[0].Job != "b" {
		t.Fatalf("%v %s", err, e.out.String())
	}
}

func TestListBackups(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("one"), "--name", "one", "--no-schedule")
	e.mustRun("add", e.src, e.tmpl("two"), "--name", "two", "--no-schedule")
	e.mustRun("list", "backups")
	if strings.TrimSpace(e.out.String()) != "no backups" {
		t.Fatal(e.out.String())
	}
	mk := func(name string, marker bool, jobName string) string {
		d := filepath.Join(e.dstP, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if marker {
			m := backupset.Marker{Job: jobName, RunID: "r1", Created: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), FileCount: 12, Bytes: 5 << 20}
			if err := backupset.WriteMarker(d, m); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	good := mk("one-20260301T000000Z", true, "one")
	mk("one-20260302T000000Z", false, "one")        // no marker
	mk("one-20260303T000000Z.partial", true, "one") // partial
	mk("two-20260301T000000Z", true, "two")
	e.mustRun("list", "backups")
	out := e.out.String()
	for _, want := range []string{"one/one-20260301T000000Z", "two/two-20260301T000000Z", "5.0 MiB", "12", good} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "20260302") || strings.Contains(out, "partial") {
		t.Fatalf("non-committed dirs listed:\n%s", out)
	}
	e.mustRun("list", "backups", "--job", "two", "--json")
	var infos []backupInfo
	if err := json.Unmarshal(e.out.Bytes(), &infos); err != nil || len(infos) != 1 || infos[0].Job != "two" || infos[0].FileCount != 12 || infos[0].RunID != "r1" {
		t.Fatalf("%v %+v", err, infos)
	}
}

func TestListBackupsUnreadableParentWarns(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.dstP), "afile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.mustRun("add", e.src, filepath.Join(filepath.Dir(e.dstP), "afile", "x-{timestamp}"), "--name", "bad", "--no-schedule")
	e.mustRun("list", "backups")
	if !strings.Contains(e.errb.String(), "job bad") {
		t.Fatalf("warning missing: %q", e.errb.String())
	}
}
