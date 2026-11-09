package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/state"
	"github.com/drizki/backup-cli/internal/systemd"
)

type env struct {
	t     *testing.T
	state string
	units string
	src   string
	dstP  string
	fake  *systemd.Fake
	deps  Deps
	out   *bytes.Buffer
	errb  *bytes.Buffer
	calls []EngineRequest
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, state: filepath.Join(root, "state"), units: filepath.Join(root, "units"),
		src: filepath.Join(root, "data"), dstP: filepath.Join(root, "bk"), fake: systemd.NewFake(),
		out: &bytes.Buffer{}, errb: &bytes.Buffer{}}
	for _, d := range []string{e.src, e.dstP} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.deps = Deps{
		Stdout: e.out, Stderr: e.errb, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Manager: func(dir string) systemd.Manager {
			e.units = dir
			return e.fake
		},
		Executable: func() (string, error) { return "/usr/local/bin/backup-cli", nil },
		Engine: func(_ context.Context, r EngineRequest) error {
			e.calls = append(e.calls, r)
			return nil
		},
	}
	return e
}

// run executes the CLI with the env's state dir and returns the exit code.
func (e *env) run(args ...string) int {
	e.t.Helper()
	e.out.Reset()
	e.errb.Reset()
	full := append([]string{"--state-dir", e.state}, args...)
	return Run(context.Background(), full, e.deps)
}

func (e *env) tmpl(name string) string { return filepath.Join(e.dstP, name+"-{timestamp}") }

func (e *env) store() state.Store { return state.New(e.state) }

func (e *env) mustRun(args ...string) {
	e.t.Helper()
	if code := e.run(args...); code != 0 {
		e.t.Fatalf("%v: exit %d stderr=%s", args, code, e.errb.String())
	}
}

func (e *env) expectCode(want int, args ...string) {
	e.t.Helper()
	if code := e.run(args...); code != want {
		e.t.Fatalf("%v: exit %d want %d\nstdout=%s\nstderr=%s", args, code, want, e.out.String(), e.errb.String())
	}
}

func (e *env) jobExists(name string) bool {
	_, err := e.store().LoadJob(name)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		e.t.Fatal(err)
	}
	return err == nil
}
