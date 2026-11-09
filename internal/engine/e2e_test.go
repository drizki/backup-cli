package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/systemd"
)

// cliRun drives the real CLI with the real engine.
func cliRun(t *testing.T, stateDir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	deps := cli.Deps{
		Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(""),
		Getenv:     func(string) string { return "" },
		Manager:    func(string) systemd.Manager { return systemd.NewFake() },
		Executable: func() (string, error) { return "/usr/local/bin/backup-cli", nil },
		Engine:     Run,
	}
	code := cli.Run(context.Background(), append([]string{"--state-dir", stateDir}, args...), deps)
	return code, out.String(), errb.String()
}

func TestEndToEndThroughTheCLI(t *testing.T) {
	root := t.TempDir()
	src, dst, stateDir := filepath.Join(root, "src"), filepath.Join(root, "bk"), filepath.Join(root, "state")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "one.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	must := func(args ...string) string {
		t.Helper()
		code, out, errb := cliRun(t, stateDir, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errb)
		}
		return out
	}
	out := must("add", src, filepath.Join(dst, "e2e-{timestamp}"), "--no-schedule", "--name", "e2e", "--now", "--keep", "5")
	if !strings.Contains(out, "committed") {
		t.Fatalf("add --now did not run the engine in the foreground:\n%s", out)
	}
	time.Sleep(1100 * time.Millisecond) // timestamps have one-second resolution
	if err := os.WriteFile(filepath.Join(src, "two.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	must("run", "e2e")

	checkListings(t, stateDir, must)
}

func checkListings(t *testing.T, stateDir string, must func(...string) string) {
	t.Helper()
	root := filepath.Dir(stateDir)
	var runs []struct {
		RunID string `json:"run_id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(must("list", "runs", "--json")), &runs); err != nil || len(runs) != 2 {
		t.Fatalf("list runs: %v %v", runs, err)
	}
	var backups []struct{ ID, Path string }
	if err := json.Unmarshal([]byte(must("list", "backups", "--json")), &backups); err != nil || len(backups) != 2 {
		t.Fatalf("list backups: %v %v", backups, err)
	}
	newest := backups[len(backups)-1]
	if got := must("verify", newest.ID); !strings.Contains(got, "ok:") {
		t.Fatalf("verify: %s", got)
	}
	if _, err := os.Stat(filepath.Join(newest.Path, manifest.SHA256Name)); err != nil {
		t.Fatal(err)
	}
	if got := must("tail", runs[0].RunID); !strings.Contains(got, "committed") {
		t.Fatalf("tail printed %q", got)
	}
	time.Sleep(1100 * time.Millisecond)
	before := snap(t, root)
	if got := must("run", "e2e", "--dry-run"); !strings.Contains(got, "nothing was written") {
		t.Fatalf("dry run: %s", got)
	}
	if d := diffSnaps(before, snap(t, root)); d != "" {
		t.Fatalf("dry run through the CLI changed files:\n%s", d)
	}

	flipByte(t, filepath.Join(newest.Path, "two.txt"))
	if code, out, _ := cliRun(t, stateDir, "verify", newest.Path); code != 1 || !strings.Contains(out, "FAILED: two.txt") {
		t.Fatalf("verify after tampering: exit %d\n%s", code, out)
	}
}
