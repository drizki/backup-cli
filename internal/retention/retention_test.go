package retention

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/runlog"
	"github.com/drizki/backup-cli/internal/safedelete"
)

type fixture struct {
	t     *testing.T
	root  string
	dst   string
	state string
	info  string
	j     job.Job
	g     safedelete.Guard
	out   *bytes.Buffer
	log   *runlog.Log
}

func newFixture(t *testing.T, keep int) *fixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: root, dst: filepath.Join(root, "bk"), state: filepath.Join(root, "state"), out: &bytes.Buffer{}}
	for _, d := range []string{f.dst, f.state, filepath.Join(root, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.info = filepath.Join(root, "mountinfo")
	if err := os.WriteFile(f.info, []byte("22 1 8:1 / / rw - ext4 /dev/sda1 rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.j = job.Job{Version: 1, Name: "docs", Source: filepath.Join(root, "data"), DstTemplate: filepath.Join(f.dst, "docs-{timestamp}"), Keep: keep}
	f.g = safedelete.Guard{StateDir: f.state, MountInfo: f.info}
	f.log = runlog.Mirror(f.out)
	return f
}

// backup creates committed backup number n (day n of March 2026).
func (f *fixture) backup(n int) string {
	f.t.Helper()
	name := fmt.Sprintf("docs-202603%02dT000000Z", n)
	dir := filepath.Join(f.dst, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	m := backupset.Marker{Job: "docs", RunID: "r", Created: time.Date(2026, 3, n, 0, 0, 0, 0, time.UTC)}
	if err := backupset.WriteMarker(dir, m); err != nil {
		f.t.Fatal(err)
	}
	return dir
}

func (f *fixture) scan() []backupset.Backup {
	f.t.Helper()
	all, err := backupset.Scan(f.j)
	if err != nil {
		f.t.Fatal(err)
	}
	return all
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func names(bs []backupset.Backup) string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Name[len("docs-202603"):len("docs-202603")+2])
	}
	return strings.Join(out, ",")
}

func TestPlanKeepsNewest(t *testing.T) {
	f := newFixture(t, 1)
	for n := 1; n <= 6; n++ {
		f.backup(n)
	}
	all := f.scan()
	for keep, want := range map[int]string{1: "01,02,03,04,05", 2: "01,02,03,04", 5: "01", 6: "", 9: "", 0: "01,02,03,04,05", -3: "01,02,03,04,05"} {
		if got := names(Plan(all, keep)); got != want {
			t.Errorf("keep %d: got %q want %q", keep, got, want)
		}
	}
}

func TestPlanOrdersByMarkerTimeNotName(t *testing.T) {
	f := newFixture(t, 1)
	a, b := f.backup(1), f.backup(2)
	// Backdate the backup whose name sorts last.
	if err := backupset.WriteMarker(b, backupset.Marker{Job: "docs", RunID: "r", Created: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	got := Plan(f.scan(), 1)
	if len(got) != 1 || got[0].Path != b {
		t.Fatalf("got %v want only %s (a=%s)", got, b, a)
	}
}

func TestPlanIgnoresNonCommitted(t *testing.T) {
	f := newFixture(t, 1)
	f.backup(1)
	f.backup(2)
	odd := filepath.Join(f.dst, "docs-20260303T000000Z") // no marker
	partial := filepath.Join(f.dst, "docs-20260304T000000Z.partial")
	bad := filepath.Join(f.dst, "docs-20260305T000000Z")
	for _, d := range []string{odd, partial, bad} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bad, ".backup-cli.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := names(Plan(f.scan(), 1)); got != "01" {
		t.Fatalf("got %q", got)
	}
	if got := Plan([]backupset.Backup{{Name: "x", Status: backupset.Committed}}, 1); got != nil {
		t.Fatal("a committed entry without a marker must be ignored")
	}
}

func TestPruneRemovesOldestAndLeavesTheRest(t *testing.T) {
	for _, keep := range []int{1, 2, 5} {
		t.Run(fmt.Sprint("keep", keep), func(t *testing.T) {
			f := newFixture(t, keep)
			var dirs []string
			for n := 1; n <= 6; n++ {
				dirs = append(dirs, f.backup(n))
			}
			unknown := filepath.Join(f.dst, "docs-20260307T000000Z") // marker-less: never touched
			partial := filepath.Join(f.dst, "docs-20260308T000000Z.partial")
			foreign := filepath.Join(f.dst, "keepme")
			for _, d := range []string{unknown, partial, foreign} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := Prune(context.Background(), f.g, f.j, f.scan(), dirs[5], f.log); err != nil {
				t.Fatal(err)
			}
			for i, d := range dirs {
				if want := i >= 6-keep; exists(d) != want {
					t.Errorf("backup %d: exists=%v want %v", i+1, exists(d), want)
				}
			}
			for _, d := range []string{unknown, partial, foreign} {
				if !exists(d) {
					t.Errorf("%s was touched", d)
				}
			}
		})
	}
}

func TestPruneNeverRemovesTheLastBackup(t *testing.T) {
	f := newFixture(t, 0) // a corrupt keep count
	only := f.backup(1)
	if err := Prune(context.Background(), f.g, f.j, f.scan(), "", f.log); err != nil || !exists(only) {
		t.Fatalf("err=%v exists=%v", err, exists(only))
	}
	newest := f.backup(2)
	if err := Prune(context.Background(), f.g, f.j, f.scan(), newest, f.log); err != nil {
		t.Fatal(err)
	}
	if exists(only) || !exists(newest) {
		t.Fatal("with keep 0 the newest backup must survive and the older one go")
	}
}

func TestPruneProtectsTheCurrentBackup(t *testing.T) {
	f := newFixture(t, 1)
	current := f.backup(1)
	f.backup(2)
	// Backdate the current backup so a clock skew would make it look oldest.
	if err := backupset.WriteMarker(current, backupset.Marker{Job: "docs", RunID: "r", Created: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	if err := Prune(context.Background(), f.g, f.j, f.scan(), current, f.log); err != nil {
		t.Fatal(err)
	}
	if !exists(current) || !strings.Contains(f.out.String(), "it is the backup just made") {
		t.Fatalf("current removed or not reported: %s", f.out.String())
	}
}

func TestPruneReportsRefusalsAndContinues(t *testing.T) {
	f := newFixture(t, 1)
	oldest := f.backup(1)
	middle := f.backup(2)
	newest := f.backup(3)
	// Break the oldest backup's ownership: its marker now names another job.
	if err := os.WriteFile(filepath.Join(oldest, backupset.MarkerName), []byte(`{"format":1,"job":"other","run_id":"r","created":"2026-03-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	all := f.scan()
	// Scan marks it foreign; hand the guard a stale view that still lists it.
	all = append(all, backupset.Backup{Name: filepath.Base(oldest), Path: oldest, Job: "docs", Status: backupset.Committed,
		Marker: &backupset.Marker{Job: "docs", Created: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}})
	err := Prune(context.Background(), f.g, f.j, all, newest, f.log)
	if !errors.Is(err, safedelete.ErrRefused) {
		t.Fatalf("expected the guard refusal, got %v", err)
	}
	if !exists(oldest) || exists(middle) || !exists(newest) {
		t.Fatal("the refused backup must stay, the others follow the plan")
	}
	if !strings.Contains(f.out.String(), "kept") {
		t.Fatal(f.out.String())
	}
}

func TestPruneStopsWhenCancelled(t *testing.T) {
	f := newFixture(t, 1)
	old := f.backup(1)
	newest := f.backup(2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Prune(ctx, f.g, f.j, f.scan(), newest, f.log); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if !exists(old) {
		t.Fatal("removed after cancellation")
	}
}

func TestFuncUsesTheStateDirGuard(t *testing.T) {
	f := newFixture(t, 1)
	old := f.backup(1)
	newest := f.backup(2)
	if err := Func(f.state)(context.Background(), f.j, f.scan(), newest, f.log); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(newest) {
		t.Fatal("Func did not prune")
	}
	// A guard whose state dir contains the backups refuses everything.
	again := f.backup(3)
	if err := Func(f.root)(context.Background(), f.j, f.scan(), again, f.log); err == nil {
		t.Fatal("expected refusals when the state dir contains the backups")
	}
	if !exists(newest) {
		t.Fatal("removed despite the state directory guard")
	}
}
