package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/exitcode"
)

func TestJobRmRemovesJobAndUnitsKeepsBackups(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("data"), "--name", "data", "--schedule", "daily,01:00")
	backup := filepath.Join(e.dstP, "data-20260101T000000Z")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(backup, "file.txt")
	if err := os.WriteFile(payload, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	e.mustRun("job", "rm", "data")
	if e.jobExists("data") {
		t.Fatal("job file still present")
	}
	if len(e.fake.Installed) != 0 || len(e.fake.Enabled) != 0 {
		t.Fatalf("units left: %v %v", e.fake.Installed, e.fake.Enabled)
	}
	if !contains(e.fake.Calls, "Disable backup-cli-data") || !contains(e.fake.Calls, "Remove backup-cli-data") {
		t.Fatalf("calls: %v", e.fake.Calls)
	}
	b, err := os.ReadFile(payload)
	if err != nil || string(b) != "keep me" {
		t.Fatalf("backup data touched: %q %v", b, err)
	}
	if _, err := os.Stat(e.src); err != nil {
		t.Fatal("source touched")
	}
	if !strings.Contains(e.out.String(), "left untouched") {
		t.Fatal(e.out.String())
	}
}

func TestJobRmEdgeCases(t *testing.T) {
	e := newEnv(t)
	e.expectCode(exitcode.Usage, "job", "rm", "ghost")
	e.expectCode(exitcode.Usage, "job", "rm", "../etc")

	// unscheduled job never had units: systemd is not touched at all
	e.mustRun("add", e.src, e.tmpl("a"), "--name", "a", "--no-schedule")
	e.mustRun("job", "rm", "a")
	if len(e.fake.Calls) != 0 || e.jobExists("a") {
		t.Fatalf("calls: %v", e.fake.Calls)
	}

	// scheduled job but systemd gone: warns, still removes the job file
	e.mustRun("add", e.src, e.tmpl("b"), "--name", "b", "--schedule", "daily,01:00")
	e.fake.Avail = false
	e.mustRun("job", "rm", "b")
	if !strings.Contains(e.errb.String(), "warning") || e.jobExists("b") {
		t.Fatalf("stderr %q", e.errb.String())
	}

	// disable failure is only a warning; remove failure keeps the job file
	e.fake.Avail = true
	e.mustRun("add", e.src, e.tmpl("c"), "--name", "c", "--schedule", "daily,01:00")
	e.fake.FailOn["Disable"] = errors.New("x")
	e.mustRun("job", "rm", "c")
	if !strings.Contains(e.errb.String(), "disable") || e.jobExists("c") {
		t.Fatal("disable failure handling")
	}
	delete(e.fake.FailOn, "Disable")
	e.mustRun("add", e.src, e.tmpl("d"), "--name", "d", "--schedule", "daily,01:00")
	e.fake.FailOn["Remove"] = errors.New("x")
	e.expectCode(exitcode.Failed, "job", "rm", "d")
	if !e.jobExists("d") {
		t.Fatal("job file removed although unit removal failed")
	}

	// corrupt job file is a failure, not "not found"
	if err := os.WriteFile(e.store().JobPath("bad"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "job", "rm", "bad")
}
