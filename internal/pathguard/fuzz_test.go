package pathguard

import (
	"path/filepath"
	"strings"
	"testing"
)

func FuzzContains(f *testing.F) {
	f.Add("/srv/data", "/srv/data-old")
	f.Add("/", "/x")
	f.Add("/a/b", "/a/b/../c")
	f.Add("/a/b", "/a/b/c/d")
	f.Add("/a//b/", "/a/b/./c")
	f.Fuzz(func(t *testing.T, a, b string) {
		if !filepath.IsAbs(a) || !filepath.IsAbs(b) || strings.ContainsRune(a+b, 0) {
			return
		}
		a, b = filepath.Clean(a), filepath.Clean(b)
		if !Contains(a, a) {
			t.Fatalf("Contains(%q,%q) false", a, a)
		}
		if Contains(a, b) && Contains(b, a) && a != b {
			t.Fatalf("mutual containment %q %q", a, b)
		}
		want := a == b || a == "/" || strings.HasPrefix(b, a+"/")
		if Contains(a, b) != want {
			t.Fatalf("Contains(%q,%q)=%v want %v", a, b, Contains(a, b), want)
		}
	})
}
