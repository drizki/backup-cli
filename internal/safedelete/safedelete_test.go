package safedelete

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const goodName = "docs-20260301T000000Z"

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestDeleteRemovesTheBackupOnly(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	keep := f.backup("docs-20260302T000000Z")
	unknown := filepath.Join(f.dst, "unrelated")
	mkdir(t, unknown)
	write(t, filepath.Join(unknown, "x"), "x")
	srcBefore := snapshot(t, f.src)
	if err := f.g.Delete(f.j, victim); err != nil {
		t.Fatal(err)
	}
	if exists(victim) {
		t.Fatal("backup still exists")
	}
	if !exists(keep) || !exists(unknown) || !exists(f.dst) {
		t.Fatal("a sibling or the destination parent was touched")
	}
	if snapshot(t, f.src) != srcBefore {
		t.Fatal("source changed")
	}
}

func TestPlanDeletesNothingAndReportsMarkerSize(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	before := snapshot(t, f.root)
	p, err := f.g.Plan(f.j, victim)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != victim || p.Files != 7 || p.Bytes != 4096 || p.Job != "docs" || p.Partial {
		t.Fatalf("%+v", p)
	}
	if snapshot(t, f.root) != before {
		t.Fatal("Plan changed the tree")
	}
}

func TestGuardNoMarker(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(f.dst, goodName)
	mkdir(t, dir)
	write(t, filepath.Join(dir, "x"), "x")
	f.expectRefused(f.g.Delete(f.j, dir), "no .backup-cli.json marker", snapshot(t, f.root))
}

func TestGuardMarkerNotJSON(t *testing.T) {
	f := newFixture(t)
	dir := f.backup(goodName)
	write(t, filepath.Join(dir, ".backup-cli.json"), "{not json")
	f.expectRefused(f.g.Delete(f.j, dir), "unusable marker", snapshot(t, f.root))
}

func TestGuardMarkerJobMismatch(t *testing.T) {
	f := newFixture(t)
	dir := f.backup(goodName)
	write(t, filepath.Join(dir, ".backup-cli.json"), `{"format":1,"job":"other","run_id":"r"}`)
	f.expectRefused(f.g.Delete(f.j, dir), `belongs to job "other"`, snapshot(t, f.root))
}

func TestGuardOutsideDestinationParent(t *testing.T) {
	f := newFixture(t)
	f.backup(goodName)
	elsewhere := filepath.Join(f.root, "elsewhere")
	mkdir(t, elsewhere)
	outside := filepath.Join(elsewhere, goodName)
	mkdir(t, outside)
	write(t, filepath.Join(outside, ".backup-cli.json"), `{"format":1,"job":"docs","run_id":"r"}`)
	f.expectRefused(f.g.Delete(f.j, outside), "not directly inside", snapshot(t, f.root))
}

func TestGuardDotDotElement(t *testing.T) {
	f := newFixture(t)
	f.backup(goodName)
	tricky := f.dst + "/../bk/" + goodName
	f.expectRefused(f.g.Delete(f.j, tricky), "contains a .. element", snapshot(t, f.root))
}

func TestGuardRelativePath(t *testing.T) {
	f := newFixture(t)
	f.backup(goodName)
	f.expectRefused(f.g.Delete(f.j, "bk/"+goodName), "not an absolute path", snapshot(t, f.root))
}

func TestGuardSymlinkedComponent(t *testing.T) {
	f := newFixture(t)
	f.backup(goodName)
	alias := filepath.Join(f.root, "alias")
	if err := os.Symlink(f.dst, alias); err != nil {
		t.Fatal(err)
	}
	f.expectRefused(f.g.Delete(f.j, filepath.Join(alias, goodName)), "not directly inside", snapshot(t, f.root))
}

func TestGuardTargetIsDestinationParent(t *testing.T) {
	f := newFixture(t)
	f.backup(goodName)
	f.expectRefused(f.g.Delete(f.j, f.dst), "not directly inside", snapshot(t, f.root))
}

func TestGuardNameMustMatchTemplate(t *testing.T) {
	f := newFixture(t)
	dir := f.backup("photos-20260301T000000Z")
	f.expectRefused(f.g.Delete(f.j, dir), "does not match the job's destination template", snapshot(t, f.root))
}

func TestGuardTargetIsSymlink(t *testing.T) {
	f := newFixture(t)
	real := f.backup("docs-20260301T000000Z")
	link := filepath.Join(f.dst, "docs-20260302T000000Z")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f.expectRefused(f.g.Delete(f.j, link), "is a symlink", snapshot(t, f.root))
}

func TestGuardTargetIsAFileOrMissing(t *testing.T) {
	f := newFixture(t)
	file := filepath.Join(f.dst, goodName)
	write(t, file, "just a file")
	f.expectRefused(f.g.Delete(f.j, file), "is not a directory", snapshot(t, f.root))
	f.expectRefused(f.g.Delete(f.j, filepath.Join(f.dst, "docs-20260309T000000Z")), "no such file", snapshot(t, f.root))
}

func TestGuardRoot(t *testing.T) {
	f := newFixture(t)
	f.backup(goodName)
	before := snapshot(t, f.root)
	f.expectRefused(f.g.Delete(f.j, "/"), "filesystem root", before)
	f.expectRefused(f.g.Delete(f.j, "//"), "filesystem root", before)
}

func TestGuardPartialAndCommittedAreNotInterchangeable(t *testing.T) {
	f := newFixture(t)
	committed := f.backup(goodName)
	part := f.partial("docs-20260302T000000Z.partial", "docs")
	before := snapshot(t, f.root)
	f.expectRefused(f.g.Delete(f.j, part), "need DeletePartial", before)
	f.expectRefused(f.g.DeletePartial(f.j, committed), "need DeletePartial", before)
}

func TestGuardStateDirectory(t *testing.T) {
	f := newFixture(t)
	dir := f.backup(goodName)
	before := snapshot(t, f.root)

	f.g.StateDir = dir // the target is the state dir
	f.expectRefused(f.g.Delete(f.j, dir), "state directory", before)

	f.g.StateDir = filepath.Join(dir, "sub") // the state dir is inside the target
	f.expectRefused(f.g.Delete(f.j, dir), "state directory", before)

	f.g.StateDir = f.dst // the target is inside the state dir
	f.expectRefused(f.g.Delete(f.j, dir), "state directory", before)

	f.g.StateDir = ""
	f.expectRefused(f.g.Delete(f.j, dir), "no state directory configured", before)
}

func TestGuardStateDirectoryViaSymlink(t *testing.T) {
	f := newFixture(t)
	dir := f.backup(goodName)
	link := filepath.Join(f.root, "state-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	f.g.StateDir = link
	f.expectRefused(f.g.Delete(f.j, dir), "state directory", snapshot(t, f.root))
}

func TestGuardMountpoints(t *testing.T) {
	f := newFixture(t)
	dir := f.backup(goodName)
	root := "22 1 8:1 / / rw - ext4 /dev/sda1 rw\n"

	f.mounts(root + "23 22 0:5 / " + dir + " rw - xfs /dev/sdb rw\n")
	f.expectRefused(f.g.Delete(f.j, dir), "mount point", snapshot(t, f.root))

	f.mounts(root + "23 22 0:5 / " + filepath.Join(dir, "sub") + " rw - xfs /dev/sdb rw\n")
	f.expectRefused(f.g.Delete(f.j, dir), "mount point", snapshot(t, f.root))

	f.g.MountInfo = filepath.Join(f.root, "no-such-mountinfo")
	f.expectRefused(f.g.Delete(f.j, dir), "cannot read the mount table", snapshot(t, f.root))
}

func TestDefaultMountInfoIsProc(t *testing.T) {
	if got := (Guard{}).mountInfo(); got != "/proc/self/mountinfo" {
		t.Fatal(got)
	}
}

func TestGuardSourceRelations(t *testing.T) {
	cases := map[string]func(f *fixture, dir string) string{
		"source equals the target":                               func(_ *fixture, dir string) string { return dir },
		"source inside the target":                               func(_ *fixture, dir string) string { return filepath.Join(dir, "sub") },
		"source is the destination parent (contains the target)": func(f *fixture, _ string) string { return f.dst },
		"source above the destination parent":                    func(f *fixture, _ string) string { return f.root },
		"source reached through a symlink": func(f *fixture, dir string) string {
			link := filepath.Join(f.root, "src-alias")
			if err := os.Symlink(dir, link); err != nil {
				f.t.Fatal(err)
			}
			return link
		},
		"source inside the target through a symlink": func(f *fixture, dir string) string {
			link := filepath.Join(f.root, "src-alias")
			if err := os.Symlink(filepath.Join(dir, "sub"), link); err != nil {
				f.t.Fatal(err)
			}
			return link
		},
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			dir := f.backup(goodName)
			f.j.Source = source(f, dir)
			f.expectRefused(f.g.Delete(f.j, dir), "the job's source", snapshot(t, f.root))
		})
	}
}

func TestAncestorOfByInode(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	mkdir(t, child)
	rootInfo, _ := os.Stat(root)
	childInfo, _ := os.Stat(child)
	if !ancestorOf(rootInfo, child) {
		t.Fatal("root is an ancestor of child")
	}
	if ancestorOf(childInfo, root) || ancestorOf(childInfo, child) {
		t.Fatal("a child or the same directory is not a proper ancestor")
	}
}

func TestDeletePartial(t *testing.T) {
	f := newFixture(t)
	part := f.partial("docs-20260302T000000Z.partial", "docs")
	committed := f.backup(goodName)
	plan, err := f.g.PlanPartial(f.j, part)
	if err != nil || !plan.Partial || plan.Files != 0 {
		t.Fatalf("%+v %v", plan, err)
	}
	if !exists(part) {
		t.Fatal("PlanPartial deleted")
	}
	if err := f.g.DeletePartial(f.j, part); err != nil {
		t.Fatal(err)
	}
	if exists(part) || !exists(committed) {
		t.Fatal("wrong directory removed")
	}
}

func TestDeletePartialGuards(t *testing.T) {
	f := newFixture(t)
	foreign := f.partial("docs-20260302T000000Z.partial", "other")
	nojournal := filepath.Join(f.dst, "docs-20260303T000000Z.partial")
	mkdir(t, nojournal)
	write(t, filepath.Join(nojournal, "x"), "x")
	wrongName := f.partial("photos-20260303T000000Z.partial", "docs")
	before := snapshot(t, f.root)
	f.expectRefused(f.g.DeletePartial(f.j, foreign), `belongs to job "other"`, before)
	f.expectRefused(f.g.DeletePartial(f.j, nojournal), "no readable journal", before)
	f.expectRefused(f.g.DeletePartial(f.j, wrongName), "does not match", before)
	f.expectRefused(f.g.DeletePartial(f.j, filepath.Join(f.root, "elsewhere.partial")), "no such file", before)
}

func TestTOCTOUSwapForSymlink(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	hold := f.backup("docs-20260302T000000Z")
	f.g.beforeRemove = func(path string) {
		must(t, os.Rename(path, filepath.Join(f.root, "moved")))
		must(t, os.Symlink(hold, path))
	}
	err := f.g.Delete(f.j, victim)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if !exists(filepath.Join(hold, "sub", "file.txt")) || !exists(filepath.Join(f.root, "moved", "sub", "file.txt")) {
		t.Fatal("data was removed after the swap")
	}
}

func TestTOCTOUSwapForDifferentDirectory(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	f.g.beforeRemove = func(path string) {
		must(t, os.Rename(path, filepath.Join(f.root, "moved")))
		mkdir(t, path)
		write(t, filepath.Join(path, "precious"), "new owner")
	}
	if err := f.g.Delete(f.j, victim); !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if !exists(filepath.Join(victim, "precious")) || !exists(filepath.Join(f.root, "moved", "sub")) {
		t.Fatal("data was removed after the swap")
	}
}

func TestTOCTOUTargetVanishes(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	f.g.beforeRemove = func(path string) { must(t, os.Rename(path, filepath.Join(f.root, "moved"))) }
	if err := f.g.Delete(f.j, victim); !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if !exists(filepath.Join(f.root, "moved", "sub", "file.txt")) {
		t.Fatal("moved data lost")
	}
}

func TestTOCTOUParentReplacedBySymlink(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	other := filepath.Join(f.root, "other-parent")
	mkdir(t, filepath.Join(other, goodName))
	write(t, filepath.Join(other, goodName, "keep"), "keep")
	f.g.beforeRemove = func(string) {
		must(t, os.Rename(f.dst, filepath.Join(f.root, "bk-moved")))
		must(t, os.Symlink(other, f.dst))
	}
	if err := f.g.Delete(f.j, victim); !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if !exists(filepath.Join(other, goodName, "keep")) || !exists(filepath.Join(f.root, "bk-moved", goodName, "sub")) {
		t.Fatal("data was removed after the parent swap")
	}
}

func TestRemoveParentUnopenable(t *testing.T) {
	f := newFixture(t)
	victim := f.backup(goodName)
	f.g.beforeRemove = func(string) { must(t, os.Rename(f.dst, filepath.Join(f.root, "gone"))) }
	if err := f.g.Delete(f.j, victim); !errors.Is(err, ErrRefused) {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
