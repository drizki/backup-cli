package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/exitcode"
)

func TestOnFailureHookFiresWhenTheRunDirectoryCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("needs a non-root user for the permission failure")
	}
	f := newFixture(t)
	fired := filepath.Join(f.root, "hook-fired")
	f.j.OnFailure = "touch " + fired
	f.save()
	runs := f.store.RunsDir()
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runs, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(runs, 0o755) }()
	err := f.run()
	f.expectCode(exitcode.Failed, err)
	if _, serr := os.Stat(fired); serr != nil {
		t.Errorf("the run failed (%v) but the on-failure hook did not fire", err)
	}
	if !strings.Contains(f.out.String(), "ERROR") {
		t.Errorf("the failure was not logged to stderr:\n%s", f.out.String())
	}
}
