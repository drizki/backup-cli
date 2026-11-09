package lock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireExcludesSecondHolder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "job.lock")
	l, err := Acquire(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(p); !errors.Is(err, ErrHeld) {
		t.Fatalf("second acquire: %v", err)
	}
	if held, err := Held(p); err != nil || !held {
		t.Fatalf("Held = %v, %v", held, err)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("pid in lock file: %q", b)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if held, _ := Held(p); held {
		t.Fatal("still held after Close")
	}
	l2, err := Acquire(p)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	_ = l2.Close()
	if _, err := os.Stat(p); err != nil {
		t.Fatal("lock file must never be deleted")
	}
}

func TestHeldDoesNotCreateFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "absent.lock")
	held, err := Held(p)
	if err != nil || held {
		t.Fatalf("%v %v", held, err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Held created the lock file")
	}
}

// TestHeldByAnotherProcess uses flock(1) in a child process.
func TestHeldByAnotherProcess(t *testing.T) {
	bin, err := exec.LookPath("flock")
	if err != nil {
		t.Skip("flock binary missing")
	}
	p := filepath.Join(t.TempDir(), "x.lock")
	cmd := exec.Command(bin, "--exclusive", p, "sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for range 200 {
		if held, _ := Held(p); held {
			break
		}
		exec.Command("sleep", "0.02").Run() //nolint:errcheck
	}
	if _, err := Acquire(p); !errors.Is(err, ErrHeld) {
		t.Fatalf("acquire while another process holds it: %v", err)
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(filepath.Join(dir, "no", "such.lock")); err == nil {
		t.Error("missing directory accepted")
	}
	if _, err := Held(filepath.Join(dir, "no", "such.lock")); err != nil {
		t.Errorf("Held on a missing directory: %v", err)
	}
	target := filepath.Join(dir, "t")
	_ = os.WriteFile(target, nil, 0o644)
	link := filepath.Join(dir, "l")
	_ = os.Symlink(target, link)
	if _, err := Acquire(link); err == nil {
		t.Error("lock file symlink followed")
	}
	if _, err := Held(link); err == nil {
		t.Error("Held followed a symlink")
	}
}
