package job

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckOverlap(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	mk := func(p ...string) string {
		d := filepath.Join(append([]string{root}, p...)...)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	src := mk("data")
	mk("bk")
	inside := mk("bk", "docs-20260101T000000Z", "nested")
	partialInside := mk("bk", "docs-20260101T000000Z.partial", "nested")
	siblingSrc := mk("bk", "photos") // shares the dst parent but is not a backup dir

	mkjob := func(source, tmpl string) Job {
		return Job{Name: "docs", Source: source, DstTemplate: tmpl, Keep: 1, Jobs: 1}
	}
	cases := []struct {
		name    string
		j       Job
		wantErr bool
	}{
		{"disjoint", mkjob(src, filepath.Join(root, "bk", "docs-{timestamp}")), false},
		{"sibling in dst parent", mkjob(siblingSrc, filepath.Join(root, "bk", "docs-{timestamp}")), false},
		{"src sibling of dst parent", mkjob(src, filepath.Join(root, "data-bk-{timestamp}")), false},
		{"dst inside src", mkjob(src, filepath.Join(src, "bk", "docs-{timestamp}")), true},
		{"dst parent is src", mkjob(src, filepath.Join(src, "docs-{timestamp}")), true},
		{"dst parent missing but inside src", mkjob(src, filepath.Join(src, "new", "deep", "docs-{timestamp}")), true},
		{"src inside backup dir", mkjob(inside, filepath.Join(root, "bk", "docs-{timestamp}")), true},
		{"src inside partial dir", mkjob(partialInside, filepath.Join(root, "bk", "docs-{timestamp}")), true},
		{"src is parent of dst parent via dotdot", mkjob(src, filepath.Join(src, "x", "..", "docs-{timestamp}")), true},
	}
	for _, c := range cases {
		err := c.j.CheckOverlap()
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}

	// Symlink aliases are resolved in both directions.
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	if err := mkjob(src, filepath.Join(alias, "docs-{timestamp}")).CheckOverlap(); err == nil {
		t.Error("dst through symlink into src not caught")
	}
	if err := mkjob(alias, filepath.Join(src, "docs-{timestamp}")).CheckOverlap(); err == nil {
		t.Error("src alias with dst inside real src not caught")
	}
	bkAlias := filepath.Join(root, "bkalias")
	if err := os.Symlink(filepath.Join(root, "bk", "docs-20260101T000000Z"), bkAlias); err != nil {
		t.Fatal(err)
	}
	if err := mkjob(filepath.Join(bkAlias, "nested"), filepath.Join(root, "bk", "docs-{timestamp}")).CheckOverlap(); err == nil {
		t.Error("src inside backup dir via symlink not caught")
	}

	// Unresolvable path (component is a regular file) is an error, not a pass.
	f := filepath.Join(root, "file")
	_ = os.WriteFile(f, nil, 0o644)
	if err := mkjob(filepath.Join(f, "x"), filepath.Join(root, "bk", "d-{timestamp}")).CheckOverlap(); err == nil {
		t.Error("unresolvable source passed")
	}
	if err := mkjob(src, filepath.Join(f, "x", "d-{timestamp}")).CheckOverlap(); err == nil {
		t.Error("unresolvable destination passed")
	}
}
