package cli

import (
	"context"
	"errors"
	"flag"
	"os/exec"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/exitcode"
)

func TestVersionAndHelp(t *testing.T) {
	e := newEnv(t)
	e.mustRun("version")
	if !strings.HasPrefix(e.out.String(), "backup-cli ") {
		t.Fatal(e.out.String())
	}
	e.mustRun("help")
	for _, c := range []string{"add", "list", "job", "run", "tail", "rm", "verify", "doctor", "version", "help", "--state-dir", "Exit codes"} {
		if !strings.Contains(e.out.String(), c) {
			t.Errorf("root help missing %q", c)
		}
	}
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		e.mustRun(args...)
		if !strings.Contains(e.out.String(), "Usage: backup-cli") {
			t.Errorf("%v: %q", args, e.out.String())
		}
	}
}

func TestEveryCommandHasHelp(t *testing.T) {
	e := newEnv(t)
	for _, args := range [][]string{
		{"help", "add"}, {"add", "--help"}, {"help", "list"}, {"help", "list", "jobs"}, {"list", "runs", "-h"},
		{"help", "list", "backups"}, {"help", "job"}, {"job", "--help"}, {"help", "job", "rm"}, {"job", "rm", "--help"},
		{"help", "run"}, {"run", "--help"}, {"help", "tail"}, {"help", "rm"}, {"help", "verify"}, {"help", "doctor"},
		{"help", "version"},
	} {
		e.mustRun(args...)
		if e.out.Len() == 0 {
			t.Errorf("%v printed nothing", args)
		}
	}
	e.mustRun("help", "add")
	for _, want := range []string{"Usage: backup-cli add [flags] <source-dir> <dst-template>", "-schedule", "-keep", "-deadline", "-no-schedule", "{timestamp}"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("add help missing %q:\n%s", want, e.out.String())
		}
	}
	e.mustRun("help", "run")
	for _, want := range []string{"-dry-run", "-from-source", "-force"} {
		if !strings.Contains(e.out.String(), want) {
			t.Errorf("run help missing %q", want)
		}
	}
	e.mustRun("list", "--help")
	if !strings.Contains(e.out.String(), "backups") {
		t.Errorf("list help: %q", e.out.String())
	}
}

func TestUsageErrors(t *testing.T) {
	e := newEnv(t)
	for name, args := range map[string][]string{
		"no command":          {},
		"unknown command":     {"bogus"},
		"unknown sub":         {"list", "bogus"},
		"missing sub":         {"list"},
		"missing job sub":     {"job"},
		"help unknown":        {"help", "bogus"},
		"help unknown sub":    {"help", "list", "bogus"},
		"version extra arg":   {"version", "x"},
		"unknown flag":        {"version", "--bogus"},
		"state-dir no value":  {"--state-dir"},
		"unit-dir no value":   {"--unit-dir"},
		"list jobs extra arg": {"list", "jobs", "x"},
		"job rm no arg":       {"job", "rm"},
	} {
		if code := e.run(args...); code != exitcode.Usage {
			t.Errorf("%s: exit %d want 2", name, code)
		}
	}
	e.expectCode(2, "bogus")
	if !strings.Contains(e.errb.String(), `unknown command "bogus"`) {
		t.Fatal(e.errb.String())
	}
}

func TestGlobalFlags(t *testing.T) {
	e := newEnv(t)
	other := e.state + "-other"
	code := Run(context.Background(), []string{"-v", "--state-dir=" + other, "--unit-dir=/u", "list", "jobs"}, e.deps)
	if code != 0 || !strings.Contains(e.out.String(), "no jobs") {
		t.Fatalf("exit %d %s", code, e.errb.String())
	}
	e.expectCode(0, "list", "jobs") // --state-dir given by the helper, no verbose
	a := &app{d: e.deps, verbose: true}
	a.debugf("hello %d", 1)
	if !strings.Contains(e.errb.String(), "debug: hello 1") {
		t.Fatalf("verbose output: %q", e.errb.String())
	}
	e.errb.Reset()
	(&app{d: e.deps}).debugf("hidden")
	if e.errb.Len() != 0 {
		t.Fatal("debug printed without --verbose")
	}
}

func TestNilDepsAreSafe(t *testing.T) {
	if code := Run(context.Background(), []string{"version"}, Deps{}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if code := Run(context.Background(), []string{"nope"}, Deps{}); code != exitcode.Usage {
		t.Fatalf("exit %d", code)
	}
}

func TestParseInterspersed(t *testing.T) {
	newFS := func() (*flagSet, *bool, *string) {
		fs := newFlagSet("t")
		b := fs.Bool("b", false, "")
		s := fs.String("s", "", "")
		return fs, b, s
	}
	cases := []struct {
		args []string
		pos  []string
		b    bool
		s    string
	}{
		{[]string{"a", "b"}, []string{"a", "b"}, false, ""},
		{[]string{"-b", "a", "-s", "x", "c"}, []string{"a", "c"}, true, "x"},
		{[]string{"a", "--s=y", "--b"}, []string{"a"}, true, "y"},
		{[]string{"-b", "--", "-s", "z"}, []string{"-s", "z"}, true, ""},
		{[]string{"a", "--", "-b"}, []string{"a", "-b"}, false, ""},
		{[]string{"-"}, []string{"-"}, false, ""},
		{nil, nil, false, ""},
	}
	for _, c := range cases {
		fs, b, s := newFS()
		pos, err := parseInterspersed(fs, c.args)
		if err != nil || strings.Join(pos, "|") != strings.Join(c.pos, "|") || *b != c.b || *s != c.s {
			t.Errorf("%v: pos=%v b=%v s=%q err=%v", c.args, pos, *b, *s, err)
		}
	}
	fs, _, _ := newFS()
	if _, err := parseInterspersed(fs, []string{"-zzz"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
	fs, _, _ = newFS()
	if _, err := parseInterspersed(fs, []string{"x", "-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v", err)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{"/usr/bin/x": "/usr/bin/x", "a b": "'a b'", "it's": `'it'\''s'`, "": "''"}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q)=%q want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB", 2 << 40: "2.0 TiB"}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d)=%q want %q", in, got, want)
		}
	}
}

func TestDefaultDepsAndRegisterEngine(t *testing.T) {
	old := registeredEngine
	defer func() { registeredEngine = old }()
	RegisterEngine(func(context.Context, EngineRequest) error { return nil })
	d := DefaultDeps()
	if d.Engine == nil || d.Manager("") == nil || d.Stdout == nil || d.Getenv == nil {
		t.Fatal("DefaultDeps incomplete")
	}
	exe, err := d.Executable()
	if err != nil || exe == "" || exe[0] != '/' {
		t.Fatalf("executable: %q %v", exe, err)
	}
	if err := d.Analyze("daily"); err != nil && !strings.Contains(err.Error(), "systemd-analyze") {
		t.Fatalf("analyze: %v", err)
	}
	if err := d.Analyze("definitely not a calendar expression ###"); err == nil {
		// Acceptable only when systemd-analyze is absent from PATH.
		if _, lerr := exec.LookPath("systemd-analyze"); lerr == nil {
			t.Fatal("systemd-analyze accepted garbage")
		}
	}
}
