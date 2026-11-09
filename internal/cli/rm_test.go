package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/journal"
	"github.com/drizki/backup-cli/internal/lock"
)

// rmEnv has job "one" and two committed backups.
type rmEnv struct {
	*env
	older, newer string
}

func newRmEnv(t *testing.T) *rmEnv {
	t.Helper()
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("one"), "--name", "one", "--no-schedule")
	r := &rmEnv{env: e}
	r.older = r.backup("one", "one-20260301T000000Z", 1)
	r.newer = r.backup("one", "one-20260302T000000Z", 2)
	return r
}

func (e *env) backup(jobName, name string, day int) string {
	e.t.Helper()
	d := filepath.Join(e.dstP, name)
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "sub", "f.txt"), []byte("payload"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	m := backupset.Marker{Job: jobName, RunID: "r", Created: time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC), FileCount: 12, Bytes: 5 << 20}
	if err := backupset.WriteMarker(d, m); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestRmYesDeletesAnOlderBackup(t *testing.T) {
	e := newRmEnv(t)
	e.mustRun("rm", "--yes", e.older)
	out := e.out.String()
	for _, want := range []string{e.older, "files:  12", "5.0 MiB", "deleted " + e.older} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if exists(e.older) || !exists(e.newer) {
		t.Fatal("wrong backup removed")
	}
}

func TestRmByIDAndUnknownArguments(t *testing.T) {
	e := newRmEnv(t)
	e.mustRun("rm", "--yes", "one/one-20260301T000000Z")
	if exists(e.older) {
		t.Fatal("id was not resolved")
	}
	e.expectCode(exitcode.Usage, "rm", "--yes", "one/one-19990101T000000Z")
	e.expectCode(exitcode.Usage, "rm", "--yes", "nonsense")
	e.expectCode(exitcode.Usage, "rm", "--yes", "nojob/x")
	e.expectCode(exitcode.Usage, "rm", "--yes")
	if !exists(e.newer) {
		t.Fatal("a failed lookup deleted something")
	}
}

func TestRmWithoutYesOnANonTerminalRefusesWithUsageError(t *testing.T) {
	e := newRmEnv(t)
	e.expectCode(exitcode.Usage, "rm", e.older)
	if !strings.Contains(e.errb.String(), "--yes") || !exists(e.older) {
		t.Fatalf("%s", e.errb.String())
	}
}

func TestRmDryRunDeletesNothing(t *testing.T) {
	e := newRmEnv(t)
	e.mustRun("rm", "--dry-run", e.older)
	if !strings.Contains(e.out.String(), "dry run: nothing was deleted") || !strings.Contains(e.out.String(), e.older) {
		t.Fatal(e.out.String())
	}
	e.mustRun("rm", "--dry-run", "--yes", e.older)
	if !exists(e.older) || !exists(e.newer) {
		t.Fatal("dry run deleted")
	}
	e.expectCode(exitcode.Usage, "rm", "--dry-run", filepath.Join(e.dstP, "nothing-here"))
}

func TestRmConfirmation(t *testing.T) {
	for _, tc := range []struct {
		input   string
		deleted bool
	}{{"y\n", true}, {"YES\n", true}, {"n\n", false}, {"\n", false}, {"", false}, {"maybe\n", false}} {
		e := newRmEnv(t)
		e.deps.IsTTY = func(io.Reader) bool { return true }
		e.deps.Stdin = strings.NewReader(tc.input)
		e.mustRun("rm", e.older)
		if exists(e.older) == tc.deleted {
			t.Errorf("input %q: deleted=%v want %v", tc.input, !exists(e.older), tc.deleted)
		}
		out := e.out.String()
		for _, want := range []string{e.older, "files:  12", "5.0 MiB", "[y/N]"} {
			if !strings.Contains(out, want) {
				t.Errorf("input %q: prompt output lacks %q:\n%s", tc.input, want, out)
			}
		}
		if !tc.deleted && !strings.Contains(out, "aborted") {
			t.Errorf("input %q: no abort message", tc.input)
		}
	}
}

func TestRmLastBackupNeedsForce(t *testing.T) {
	e := newRmEnv(t)
	e.mustRun("rm", "--yes", e.older)
	e.expectCode(exitcode.Refused, "rm", "--yes", e.newer)
	if !strings.Contains(e.errb.String(), "last good backup") || !exists(e.newer) {
		t.Fatalf("%s", e.errb.String())
	}
	e.expectCode(exitcode.Refused, "rm", "--dry-run", e.newer)
	e.mustRun("rm", "--yes", "--force", e.newer)
	if exists(e.newer) {
		t.Fatal("--force did not delete the last backup")
	}
}

func TestRmForceStillObeysEveryGuard(t *testing.T) {
	e := newRmEnv(t)
	e.mustRun("rm", "--yes", e.older) // newer is now the last backup

	// A symlink to the backup is refused and nothing is followed.
	link := filepath.Join(e.dstP, "one-20260303T000000Z")
	if err := os.Symlink(e.newer, link); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "rm", "--yes", "--force", link)
	if !exists(link) || !exists(e.newer) {
		t.Fatal("symlink case changed something")
	}

	// A marker naming a different job is refused: no such job.
	other := e.backup("ghost", "one-20260304T000000Z", 4)
	e.expectCode(exitcode.Refused, "rm", "--yes", "--force", other)
	if !strings.Contains(e.errb.String(), "no longer exists") || !exists(other) {
		t.Fatal(e.errb.String())
	}

	// A directory without a marker is refused.
	bare := filepath.Join(e.dstP, "one-20260305T000000Z")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "rm", "--yes", "--force", bare)

	// A job edited so its source contains the backups is refused by the guard.
	jobPath := e.store().JobPath("one")
	b, _ := os.ReadFile(jobPath)
	edited := strings.Replace(string(b), `"source": "`+e.src+`"`, `"source": "`+e.dstP+`"`, 1)
	if edited == string(b) {
		t.Fatalf("job file format changed:\n%s", b)
	}
	if err := os.WriteFile(jobPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "rm", "--yes", "--force", e.newer)
	if !strings.Contains(e.errb.String(), "job's source") || !exists(e.newer) {
		t.Fatal(e.errb.String())
	}
}

func TestRmPartialDirectory(t *testing.T) {
	e := newRmEnv(t)
	part := filepath.Join(e.dstP, "one-20260310T000000Z.partial")
	if err := os.MkdirAll(part, 0o755); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "rm", "--yes", part) // no journal
	jr, err := journal.Open(filepath.Join(part, journal.Name), "one", "r1")
	if err != nil {
		t.Fatal(err)
	}
	_ = jr.Close()
	e.mustRun("rm", "--dry-run", part)
	if !strings.Contains(e.out.String(), "partial backup") || !exists(part) {
		t.Fatal(e.out.String())
	}
	e.mustRun("rm", "--yes", part)
	if exists(part) || !exists(e.older) || !exists(e.newer) {
		t.Fatal("wrong directory removed")
	}
}

func TestRmUnusableJobFile(t *testing.T) {
	e := newRmEnv(t)
	if err := os.WriteFile(e.store().JobPath("one"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "rm", "--yes", e.older)
	if !exists(e.older) {
		t.Fatal("deleted despite an unreadable job")
	}
}

func TestIsTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) || isTerminal(strings.NewReader("")) {
		t.Fatal("neither /dev/null nor a string reader is a terminal")
	}
	e := newEnv(t)
	a := &app{d: e.deps}
	if a.isTTY() {
		t.Fatal("no IsTTY func means not a terminal")
	}
}

func TestRmRefusesWhileTheJobIsRunning(t *testing.T) {
	e := newRmEnv(t)
	l, err := lock.Acquire(e.store().LockPath("one"))
	if err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "rm", "--yes", e.older)
	if !exists(e.older) {
		t.Fatal("deleted while the job lock was held")
	}
	e.mustRun("rm", "--dry-run", e.older) // a preview does not need the lock
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	e.mustRun("rm", "--yes", e.older)
	if exists(e.older) {
		t.Fatal("not deleted after the lock was released")
	}
}
