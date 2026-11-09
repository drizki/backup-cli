package doctor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/memlimit"
)

func (f *fixture) backupWithFiles(files int64) {
	f.t.Helper()
	dir := filepath.Join(f.dst, "docs-20260301T000000Z")
	mkdir(f.t, dir)
	m := backupset.Marker{Job: "docs", RunID: "r", Created: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), FileCount: files}
	if err := backupset.WriteMarker(dir, m); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) memoryMax(content string) {
	f.t.Helper()
	mkdir(f.t, filepath.Join(f.sys, "fs/cgroup"))
	write(f.t, filepath.Join(f.sys, "fs/cgroup/memory.max"), content)
}

func TestMemoryCheckWarnsWhenTheTreeCannotFitTheLimit(t *testing.T) {
	f := newFixture(t)
	f.backupWithFiles(2_000_000)
	f.memoryMax("2147483648\n") // 2 GiB, but 2 M files need about 2.6 GiB
	expect(t, f.run(), "memory", "docs", Warn, "memory limit is 2.0 GiB")
}

func TestMemoryCheckPassesWhenTheTreeFits(t *testing.T) {
	f := newFixture(t)
	f.backupWithFiles(100_000)
	f.memoryMax("2147483648\n")
	expect(t, f.run(), "memory", "docs", OK, "100000 file(s)")
	if memlimit.PeakPerFile*100_000 > 2<<30 {
		t.Fatal("fixture assumes 100000 files fit 2 GiB")
	}
}

func TestMemoryCheckIsSilentWithoutALimitOrABackup(t *testing.T) {
	f := newFixture(t)
	f.backupWithFiles(50_000_000)
	for _, c := range f.run().Checks {
		if c.Name == "memory" {
			t.Fatalf("no cgroup limit, yet: %+v", c)
		}
	}
	f2 := newFixture(t)
	f2.memoryMax("max\n")
	f2.backupWithFiles(50_000_000)
	for _, c := range f2.run().Checks {
		if c.Name == "memory" {
			t.Fatalf("unlimited cgroup, yet: %+v", c)
		}
	}
	f3 := newFixture(t)
	f3.memoryMax("1000\n")
	for _, c := range f3.run().Checks {
		if c.Name == "memory" {
			t.Fatalf("no backup yet, yet: %+v", c)
		}
	}
}
