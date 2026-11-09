package safedelete

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anyReceiver are selector names that remove filesystem entries on any
// receiver (os, syscall, unix, an os.Root, a wrapper). "Remove" alone is a
// common method name (the systemd Manager has one), so it only counts on the
// os, syscall and unix packages.
var anyReceiver = map[string]bool{"RemoveAll": true, "Unlink": true, "Unlinkat": true, "Rmdir": true}

var removePackages = map[string]bool{"os": true, "syscall": true, "unix": true}

// shellDeleters are program names that must never be spawned to delete.
var shellDeleters = map[string]bool{"rm": true, "rmdir": true, "unlink": true, "shred": true, "find": true}

// allowed lists the non-test files, outside safedelete, that may remove
// something, with the maximum number of calls and the reason. safedelete
// itself cannot be used there because it imports packages that import these
// ones (an import cycle), and each call removes only an object the caller
// created itself.
var allowed = map[string]struct {
	calls  int
	reason string
}{
	"internal/state/state.go":     {2, "RemoveJob deletes the single job definition file it owns; the atomic-write helper removes its own just-created temp file on error"},
	"internal/systemd/real.go":    {1, "Remove deletes the two unit files this tool generated (<unit-dir>/backup-cli-<job>.service and .timer)"},
	"internal/journal/journal.go": {1, "Discard removes the run's own journal file after lstat shows a regular file, once the backup is committed"},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("not the repository root: %v", err)
	}
	return root
}

// deletionCalls lists deleting selectors and spawned deleting programs in src.
func deletionCalls(t *testing.T, path string) (calls []string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if isDeleter(x) {
				calls = append(calls, fset.Position(x.Pos()).String()+" "+x.Sel.Name)
			}
		case *ast.CallExpr:
			if prog := spawnedDeleter(x); prog != "" {
				calls = append(calls, fset.Position(x.Pos()).String()+" program "+prog)
			}
		}
		return true
	})
	return calls
}

func isDeleter(x *ast.SelectorExpr) bool {
	if anyReceiver[x.Sel.Name] {
		return true
	}
	pkg, ok := x.X.(*ast.Ident)
	return ok && x.Sel.Name == "Remove" && removePackages[pkg.Name]
}

// spawnedDeleter returns the program name when call is exec.Command or
// exec.CommandContext with a deleting program as a literal argument.
func spawnedDeleter(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
		return ""
	}
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING && shellDeleters[strings.Trim(lit.Value, "\"`")] {
			return lit.Value
		}
	}
	return ""
}

func TestNoDeletionOutsideSafedelete(t *testing.T) {
	root := repoRoot(t)
	seen := map[string]int{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() && (rel == ".git" || rel == "internal/safedelete" || strings.HasSuffix(rel, "testdata")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		calls := deletionCalls(t, p)
		seen[rel] = len(calls)
		if len(calls) > allowed[rel].calls {
			t.Errorf("%s: deletion outside internal/safedelete (allowed %d):\n  %s", rel, allowed[rel].calls, strings.Join(calls, "\n  "))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, a := range allowed {
		if a.reason == "" {
			t.Errorf("allowlist entry %s has no justification", rel)
		}
		if seen[rel] != a.calls {
			t.Errorf("allowlist entry %s expects %d call(s) but found %d; tighten or remove the entry", rel, a.calls, seen[rel])
		}
	}
}

func TestGuardTestDetectsDeletion(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.go")
	code := "package x\nimport (\"os\"; \"os/exec\")\nfunc f(r *os.Root) {\n_ = os.Remove(\"a\")\n_ = os.RemoveAll(\"a\")\n_ = r.RemoveAll(\"a\")\n_ = exec.Command(\"rm\", \"-rf\", \"a\")\n}\n"
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := len(deletionCalls(t, src)); got != 4 {
		t.Fatalf("expected 4 findings, got %d", got)
	}
}
