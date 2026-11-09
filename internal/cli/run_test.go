package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/state"
)

func (e *env) addJob() {
	e.t.Helper()
	e.mustRun("add", e.src, e.tmpl("data"), "--no-schedule", "--name", "data", "--keep", "3")
	e.calls = nil
}

func TestRunPassesFlagsToTheEngine(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	e.mustRun("run", "data")
	if len(e.calls) != 1 {
		t.Fatalf("%d engine calls", len(e.calls))
	}
	r := e.calls[0]
	if r.Job.Name != "data" || r.DryRun || r.FromSource || r.Force || r.Jobs != 0 || r.VerifySample != -1 || r.Store.Dir != e.state {
		t.Fatalf("defaults: %+v", r)
	}
	if r.Stdout == nil || r.Stderr == nil {
		t.Fatal("engine writers missing")
	}
}

func TestRunFlagsReachTheEngine(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	e.mustRun("run", "--dry-run", "--from-source", "--force", "--jobs", "2", "--verify-sample", "0", "data")
	r := e.calls[0]
	if !r.DryRun || !r.FromSource || !r.Force || r.Jobs != 2 || r.VerifySample != 0 {
		t.Fatalf("flags: %+v", r)
	}
}

func TestRunUsageErrors(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	for _, args := range [][]string{
		{"run", "nope"}, {"run"}, {"run", "a", "b"}, {"run", "--jobs", "-1", "data"},
		{"run", "--verify-sample", "101", "data"}, {"run", "--verify-sample", "-5", "data"}, {"run", "--bogus", "data"},
	} {
		e.expectCode(exitcode.Usage, args...)
	}
	if len(e.calls) != 0 {
		t.Fatal("the engine ran despite usage errors")
	}
}

func TestRunPropagatesEngineExitCodes(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	for _, code := range []int{exitcode.Failed, exitcode.Partial, exitcode.Refused} {
		e.deps.Engine = func(context.Context, EngineRequest) error { return exitcode.With(code, errors.New("engine says no")) }
		e.expectCode(code, "run", "data")
		if !strings.Contains(e.errb.String(), "engine says no") {
			t.Errorf("error text missing: %q", e.errb.String())
		}
	}
}

func TestRunWithoutEngineAndCorruptJob(t *testing.T) {
	e := newEnv(t)
	e.addJob()
	e.deps.Engine = nil
	e.expectCode(exitcode.Usage, "run", "data")
	if !strings.Contains(e.errb.String(), ErrUnavailable.Error()) {
		t.Fatal(e.errb.String())
	}
	if err := state.WriteFileAtomic(e.store().JobPath("broken"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "run", "broken")
}

func TestRunHelpListsEveryFlag(t *testing.T) {
	e := newEnv(t)
	e.mustRun("run", "--help")
	for _, flag := range []string{"-dry-run", "-from-source", "-force", "-jobs", "-verify-sample"} {
		if !strings.Contains(e.out.String(), flag) {
			t.Errorf("help lacks %s", flag)
		}
	}
}
