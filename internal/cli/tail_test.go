package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/state"
)

func (e *env) writeRun(id, st, logText string) state.Status {
	e.t.Helper()
	s := state.Status{RunID: id, Job: "data", State: st, PID: 0, Log: filepath.Join(e.store().RunDir(id), "run.log")}
	if err := e.store().WriteStatus(s); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(s.Log, []byte(logText), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestTailPrintsFinishedRunLogAndReturns(t *testing.T) {
	e := newEnv(t)
	e.writeRun("r1", state.StateSucceeded, "line one\nline two\n")
	e.mustRun("tail", "r1")
	if e.out.String() != "line one\nline two\n" {
		t.Fatalf("%q", e.out.String())
	}
}

func TestTailFollowsAGrowingLogUntilTerminal(t *testing.T) {
	old := tailPoll
	tailPoll = 5 * time.Millisecond
	defer func() { tailPoll = old }()
	e := newEnv(t)
	s := e.writeRun("r2", state.StateRunning, "first\n")
	s.PID = os.Getpid() // a live process
	if err := e.store().WriteStatus(s); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- e.run("tail", "r2") }()

	appendLog := func(text string) {
		f, err := os.OpenFile(s.Log, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = f.WriteString(text)
		_ = f.Close()
	}
	time.Sleep(40 * time.Millisecond)
	select {
	case code := <-done:
		t.Fatalf("tail returned (%d) while the run was still active", code)
	default:
	}
	appendLog("second\n")
	time.Sleep(40 * time.Millisecond)
	appendLog("third\n")
	s.State = state.StateSucceeded
	if err := e.store().WriteStatus(s); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tail did not return after the run finished")
	}
	if e.out.String() != "first\nsecond\nthird\n" {
		t.Fatalf("output %q", e.out.String())
	}
}

func TestTailWaitsForALogThatAppearsLater(t *testing.T) {
	old := tailPoll
	tailPoll = 5 * time.Millisecond
	defer func() { tailPoll = old }()
	e := newEnv(t)
	s := state.Status{RunID: "r3", Job: "data", State: state.StateRunning, PID: os.Getpid()}
	if err := e.store().WriteStatus(s); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	finished := false
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(e.store().RunDir("r3"), "run.log"), []byte("late log\n"), 0o644)
		s.State = state.StateFailed
		_ = e.store().WriteStatus(s)
	}()
	err := follow(context.Background(), &out, filepath.Join(e.store().RunDir("r3"), "run.log"), func() (bool, error) {
		st, err := e.store().ReadStatus("r3")
		finished = st.Terminal()
		return finished, err
	})
	if err != nil || out.String() != "late log\n" {
		t.Fatalf("%q %v", out.String(), err)
	}
}

func TestTailDetectsVanishedProcess(t *testing.T) {
	e := newEnv(t)
	s := e.writeRun("r4", state.StateRunning, "starting\n")
	s.PID = 0x7ffffffe // no such process
	if err := e.store().WriteStatus(s); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "tail", "r4")
	if !strings.Contains(e.errb.String(), "no longer exists") || !strings.Contains(e.out.String(), "starting") {
		t.Fatalf("out=%q err=%q", e.out.String(), e.errb.String())
	}
}

func TestTailErrors(t *testing.T) {
	e := newEnv(t)
	e.expectCode(exitcode.Usage, "tail", "missing")
	e.expectCode(exitcode.Failed, "tail", "../x")
	if err := state.WriteFileAtomic(e.store().StatusPath("bad"), []byte("{"), 0o644); err == nil {
		t.Fatal("expected missing run dir")
	}
	if err := os.MkdirAll(e.store().RunDir("bad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteFileAtomic(e.store().StatusPath("bad"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "tail", "bad")
}

func TestTailFallsBackToDefaultLogPathAndStopsOnCancel(t *testing.T) {
	old := tailPoll
	tailPoll = 5 * time.Millisecond
	defer func() { tailPoll = old }()
	e := newEnv(t)
	s := state.Status{RunID: "r5", Job: "data", State: state.StateRunning, PID: os.Getpid()}
	if err := e.store().WriteStatus(s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.store().RunDir("r5"), "run.log"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	code := Run(ctx, []string{"--state-dir", e.state, "tail", "r5"}, e.deps)
	if code != 0 || e.out.String() != "hello\n" {
		t.Fatalf("code=%d out=%q", code, e.out.String())
	}
}
