package pathguard

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func realTemp(t *testing.T) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOverlaps(t *testing.T) {
	root := realTemp(t)
	src := mk(t, filepath.Join(root, "src"))
	other := mk(t, filepath.Join(root, "other"))
	sibling := mk(t, filepath.Join(root, "src2")) // prefix-sharing sibling, not inside

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"same", src, src, true},
		{"dst inside src", src, filepath.Join(src, "bk"), true},
		{"src inside dst", filepath.Join(src, "deep", "x"), src, true},
		{"disjoint", src, other, false},
		{"prefix sibling", src, sibling, false},
		{"missing dst nested in src", src, filepath.Join(src, "a", "b", "c"), true},
		{"missing dst elsewhere", src, filepath.Join(other, "a", "b"), false},
		{"dotdot", src, filepath.Join(other, "..", "src", "x"), true},
		{"root contains all", "/", src, true},
	}
	for _, c := range cases {
		got, err := Overlaps(c.a, c.b)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
		back, _ := Overlaps(c.b, c.a)
		if back != got {
			t.Errorf("%s: not symmetric", c.name)
		}
	}
}

func TestOverlapsSymlinks(t *testing.T) {
	root := realTemp(t)
	src := mk(t, filepath.Join(root, "src"))
	other := mk(t, filepath.Join(root, "other"))

	// dst is reached through a symlink that points into src.
	link := filepath.Join(other, "link")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	got, err := Overlaps(src, filepath.Join(link, "backups", "new"))
	if err != nil || !got {
		t.Fatalf("dst via symlink into src: got %v err %v", got, err)
	}
	// src reached through a symlink, dst real and inside.
	alias := filepath.Join(other, "alias")
	if err := os.Symlink(src, alias); err != nil {
		t.Fatal(err)
	}
	got, err = Overlaps(alias, filepath.Join(src, "inner"))
	if err != nil || !got {
		t.Fatalf("src alias: got %v err %v", got, err)
	}
	// Symlink to an unrelated dir must not overlap.
	far := mk(t, filepath.Join(root, "far"))
	l2 := filepath.Join(other, "farlink")
	if err := os.Symlink(far, l2); err != nil {
		t.Fatal(err)
	}
	if got, err := Overlaps(src, filepath.Join(l2, "x")); err != nil || got {
		t.Fatalf("unrelated symlink flagged: %v %v", got, err)
	}
}

func TestResolveRelativeAndErrors(t *testing.T) {
	root := realTemp(t)
	t.Chdir(root)
	r, err := Resolve("a/b")
	if err != nil || r != filepath.Join(root, "a", "b") {
		t.Fatalf("got %q %v", r, err)
	}
	// A regular file used as a directory component makes resolution fail.
	f := filepath.Join(root, "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(filepath.Join(f, "child")); err == nil {
		t.Fatal("expected error through regular file")
	}
	if _, err := Overlaps(filepath.Join(f, "child"), root); err == nil {
		t.Fatal("expected overlap error")
	}
	if _, err := Overlaps(root, filepath.Join(f, "child")); err == nil {
		t.Fatal("expected overlap error (second arg)")
	}
}

func TestContains(t *testing.T) {
	if !Contains("/a", "/a") || !Contains("/a", "/a/b") || Contains("/a", "/ab") || Contains("/a/b", "/a") {
		t.Fatal("Contains wrong")
	}
	if !Contains("/", "/x") {
		t.Fatal("root should contain")
	}
}
