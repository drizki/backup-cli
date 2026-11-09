package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunVersionAndUsage(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()

	os.Args = []string{"backup-cli", "version"}
	if code := run(); code != 0 {
		t.Fatalf("version exit %d", code)
	}
	os.Args = []string{"backup-cli", "no-such-command"}
	if code := run(); code != 2 {
		t.Fatalf("unknown command exit %d, want 2", code)
	}
}

// TestEveryCommandRunsThroughTheRealWiring drives the registered engine and
// every command against temp dirs, so a command that is not implemented
// (exit 2 with an "unavailable" message) cannot slip through.
func TestEveryCommandRunsThroughTheRealWiring(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	root := t.TempDir()
	src, dst, st := filepath.Join(root, "src"), filepath.Join(root, "bk"), filepath.Join(root, "state")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := [][]string{
		{"add", src, filepath.Join(dst, "m-{timestamp}"), "--name", "m", "--no-schedule"},
		{"run", "m", "--dry-run"},
		{"run", "m"},
		{"list", "jobs"}, {"list", "runs"}, {"list", "backups"},
		{"rm", "--dry-run", "--force", "m/" + "none"},
		{"doctor", "--json"},
		{"job", "rm", "m"},
	}
	for _, args := range steps {
		os.Args = append([]string{"backup-cli", "--state-dir", st}, args...)
		code := run()
		want := 0
		if args[0] == "rm" {
			want = 2 // a missing backup id is an ordinary usage error, not "unavailable"
		}
		if code != want {
			t.Fatalf("%v: exit %d, want %d", args, code, want)
		}
	}
}
