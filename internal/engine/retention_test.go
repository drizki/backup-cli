package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/retention"
)

func names(f *fixture) []string {
	var out []string
	for _, b := range f.committed() {
		out = append(out, b.Name)
	}
	return out
}

func TestRetentionPrunesTheOldestAfterASuccessfulRun(t *testing.T) {
	f := newFixture(t)
	f.j.Keep = 2
	f.save()
	f.e.Retention = retention.Func(f.store.Dir)
	f.mustRun()
	f.mustRun()
	both := names(f)
	if len(both) != 2 {
		t.Fatalf("nothing may be pruned within keep: %v", both)
	}
	f.mustRun()
	after := names(f)
	if len(after) != 2 || after[0] != both[1] || after[1] == both[0] || after[1] == both[1] {
		t.Fatalf("before %v after %v: the oldest must go and the new one stay", both, after)
	}
	if !strings.Contains(f.out.String(), "retention: removed "+both[0]) {
		t.Fatalf("log lacks the retention line:\n%s", f.out.String())
	}
	f.sameAsSource(f.latest())
}

func TestRetentionNeverRunsAfterAFailedPartialOrRefusedRun(t *testing.T) {
	f := newFixture(t)
	f.j.Keep = 1
	f.save()
	f.e.Retention = retention.Func(f.store.Dir)
	f.mustRun()
	f.mustRun()
	kept := names(f)
	if len(kept) != 1 {
		t.Fatalf("keep 1 must leave one backup: %v", kept)
	}

	f.j.Deadline = "1ns" // partial
	f.save()
	f.expectCode(exitcode.Partial, f.run())
	f.j.Deadline = ""
	f.j.Source = filepath.Join(f.root, "missing") // refused
	f.save()
	f.expectCode(exitcode.Refused, f.run())
	if got := names(f); len(got) != 1 || got[0] != kept[0] {
		t.Fatalf("a non-successful run changed the backups: %v", got)
	}
}

func TestRetentionNeverTouchesUnknownDirectories(t *testing.T) {
	f := newFixture(t)
	f.j.Keep = 1
	f.save()
	f.e.Retention = retention.Func(f.store.Dir)
	unknown := filepath.Join(f.dst, "docs-20200101T000000Z") // matches the template, has no marker
	if err := os.MkdirAll(unknown, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.dst, "holiday-photos")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	f.mustRun()
	f.mustRun()
	for _, d := range []string{unknown, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed: %v", d, err)
		}
	}
}

func TestDryRunPrunePlanUsesTheRetentionPlanner(t *testing.T) {
	f := newFixture(t)
	f.j.Keep = 2
	f.save()
	f.mustRun()
	f.mustRun()
	oldest := names(f)[0]
	f.out.Reset()
	f.mustRun(func(r *cli.EngineRequest) { r.DryRun = true })
	if !strings.Contains(f.out.String(), "would remove 1 old backup(s)") || !strings.Contains(f.out.String(), oldest) {
		t.Fatalf("dry run plan:\n%s", f.out.String())
	}
	if len(names(f)) != 2 {
		t.Fatal("a dry run removed a backup")
	}
}

func TestOverlapRefusedInBothDirectionsViaSymlinkAndHandEditedJob(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	src := filepath.Join(root, "data")
	if err := os.MkdirAll(filepath.Join(src, "in"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	// Each job file is written by hand, bypassing the checks of `add`.
	cases := map[string]struct{ source, template string }{
		"destination inside source":      {src, filepath.Join(src, "in", "h-{timestamp}")},
		"destination inside source link": {src, filepath.Join(alias, "in", "h-{timestamp}")},
		"source inside destination":      {filepath.Join(root, "bk", "h-20260101T000000Z", "data"), filepath.Join(root, "bk", "h-{timestamp}")},
		"source is the destination":      {src, filepath.Join(src, "h-{timestamp}")},
	}
	if err := os.MkdirAll(filepath.Join(root, "bk", "h-20260101T000000Z", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, c := range cases {
		doc := map[string]any{"version": 1, "name": "h", "source": c.source, "dst_template": c.template, "keep": 3,
			"jobs": 1, "verify_sample_pct": 2, "created": "2026-01-01T00:00:00Z"}
		b, _ := json.Marshal(doc)
		if err := os.WriteFile(filepath.Join(stateDir, "jobs", "h.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, errb := cliRun(t, stateDir, "run", "h")
		if code != exitcode.Refused {
			t.Errorf("%s: exit %d, want 4; stderr: %s", name, code, errb)
		}
	}
	if _, err := os.Stat(filepath.Join(src, "in", "h-20260101T000000Z")); err == nil {
		t.Error("a refused run created a backup")
	}
}

func TestRetentionKeepsTheNewBackupWithAnUncleanTemplateAndBackwardClock(t *testing.T) {
	f := newFixture(t)
	f.j.DstTemplate = f.dst + "//docs-{timestamp}"
	f.j.Keep = 1
	f.save()
	f.e.Retention = retention.Func(f.store.Dir)
	f.now = baseTime.AddDate(1, 0, 0) // clock ahead
	f.mustRun()
	f.now = baseTime // clock corrected
	f.step = time.Hour
	f.mustRun()
	if _, err := os.Lstat(f.j.DstFor(baseTime.Add(time.Hour))); err != nil {
		t.Fatalf("the backup just made was removed by retention: %v\n%s", err, f.out.String())
	}
}
