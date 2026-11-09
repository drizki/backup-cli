package systemd

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/schedule"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s mismatch\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestGoldenUnits(t *testing.T) {
	specs := map[string]string{
		"daily":      "daily,02:30",
		"weekly":     "weekly,sun,03:15",
		"biweekly":   "biweekly,sat,04:00",
		"monthly":    "monthly,15,05:45",
		"oncalendar": "oncalendar:Mon..Fri *-*-* 01:00:00",
	}
	for name, spec := range specs {
		r, err := schedule.Parse(spec, schedule.Options{})
		if err != nil {
			t.Fatal(err)
		}
		u := Unit{Job: "docs", Binary: "/usr/local/bin/backup-cli", StateDir: "/var/lib/backup-cli", OnCalendar: r.OnCalendar}
		golden(t, name+".service", u.ServiceText())
		golden(t, name+".timer", u.TimerText())
	}
}

func TestServiceHardening(t *testing.T) {
	s := Unit{Job: "j", Binary: "/opt/my tools/backup-cli", StateDir: "/var/lib/a b/%x$y", OnCalendar: "daily"}.ServiceText()
	want := `ExecStart="/opt/my tools/backup-cli" --state-dir "/var/lib/a b/%%x$$y" run j`
	if !strings.Contains(s, want) {
		t.Fatalf("quoting wrong:\n%s", s)
	}
	for _, need := range []string{"Type=oneshot", "Nice=19", "IOSchedulingClass=idle"} {
		if !strings.Contains(s, need) {
			t.Errorf("missing %s", need)
		}
	}
	if strings.Contains(s, "/home/") {
		t.Fatal("hardcoded user path")
	}
	tm := Unit{Job: "j", OnCalendar: "daily"}.TimerText()
	for _, need := range []string{"Persistent=true", "RandomizedDelaySec=0", "OnCalendar=daily", "Unit=backup-cli-j.service"} {
		if !strings.Contains(tm, need) {
			t.Errorf("timer missing %s", need)
		}
	}
	if quoteArg("") != `""` || quoteArg(`a"b c`) != `"a\"b c"` || quoteArg(`p\q r`) != `"p\\q r"` {
		t.Fatal("quoteArg")
	}
	if UnitName("x") != "backup-cli-x" {
		t.Fatal("unit name")
	}
}

type rec struct {
	calls []string
	out   string
	err   error
}

func (r *rec) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return []byte(r.out), r.err
}

func newReal(t *testing.T, r *rec) *Real {
	t.Helper()
	m := NewReal(t.TempDir())
	m.Run = r.run
	return m
}

func TestRealLifecycle(t *testing.T) {
	r := &rec{}
	m := newReal(t, r)
	u := Unit{Job: "docs", Binary: "/usr/local/bin/backup-cli", StateDir: "/var/lib/backup-cli", OnCalendar: "daily"}
	if err := m.Install(u); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"backup-cli-docs.service", "backup-cli-docs.timer"} {
		fi, err := os.Stat(filepath.Join(m.UnitDir, f))
		if err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("%s: %v %v", f, fi, err)
		}
	}
	if err := m.Enable(u.Name()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(u.Name()); err != nil {
		t.Fatal(err)
	}
	if err := m.Disable(u.Name()); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(u.Name()); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(u.Name()); err != nil { // already gone: still fine
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(m.UnitDir)
	if len(ents) != 0 {
		t.Fatalf("unit files left: %v", ents)
	}
	timerPath := filepath.Join(m.UnitDir, "backup-cli-docs.timer")
	want := []string{
		"systemctl daemon-reload",
		"systemctl enable --now " + timerPath, // non-default unit dir links by path
		"systemctl start --no-block backup-cli-docs.service",
		"systemctl disable --now backup-cli-docs.timer",
		"systemctl daemon-reload",
		"systemctl daemon-reload",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls:\n%s", strings.Join(r.calls, "\n"))
	}
}

func TestRealDefaultDirEnablesByName(t *testing.T) {
	r := &rec{}
	m := NewReal("")
	m.Run = r.run
	if m.UnitDir != DefaultUnitDir {
		t.Fatal(m.UnitDir)
	}
	if err := m.Enable("backup-cli-x"); err != nil {
		t.Fatal(err)
	}
	if r.calls[0] != "systemctl enable --now backup-cli-x.timer" {
		t.Fatal(r.calls[0])
	}
}

func TestRealErrors(t *testing.T) {
	r := &rec{err: errors.New("boom"), out: "detail"}
	m := newReal(t, r)
	if err := m.Install(Unit{Job: "j", OnCalendar: "daily"}); err == nil || !strings.Contains(err.Error(), "detail") {
		t.Fatalf("got %v", err)
	}
	if err := m.Enable("x"); err == nil {
		t.Fatal("enable error swallowed")
	}
	if err := m.Disable("x"); err == nil {
		t.Fatal("disable error swallowed")
	}
	if err := m.Start("x"); err == nil {
		t.Fatal("start error swallowed")
	}
	if _, err := m.IsActive("x"); err == nil {
		t.Fatal("non-exit error swallowed")
	}
	// Unit dir that is a file: Install fails before touching systemctl.
	f := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(f, nil, 0o644)
	bad := NewReal(filepath.Join(f, "sub"))
	bad.Run = (&rec{}).run
	if err := bad.Install(Unit{Job: "j"}); err == nil {
		t.Fatal("expected mkdir error")
	}
	// Remove surfaces non-ENOENT errors (path component is a file).
	if err := bad.Remove("j"); err == nil {
		t.Fatal("expected remove error")
	}
}

func TestRealIsActive(t *testing.T) {
	m := newReal(t, &rec{out: "active\n"})
	if ok, err := m.IsActive("x"); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	m = newReal(t, &rec{out: "inactive\n"})
	if ok, err := m.IsActive("x"); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}
	// A real non-zero exit (ExitError) means inactive.
	exit := exec.Command("sh", "-c", "exit 3").Run()
	m = newReal(t, &rec{err: exit})
	if ok, err := m.IsActive("x"); err != nil || ok {
		t.Fatalf("exit error: %v %v", ok, err)
	}
}

func TestRealAvailable(t *testing.T) {
	m := NewReal(t.TempDir())
	m.RuntimeDir = t.TempDir()
	m.LookPath = func(string) (string, error) { return "/bin/systemctl", nil }
	if !m.Available() {
		t.Fatal("should be available")
	}
	m.LookPath = func(string) (string, error) { return "", errors.New("missing") }
	if m.Available() {
		t.Fatal("no systemctl")
	}
	m.LookPath = func(string) (string, error) { return "/bin/systemctl", nil }
	m.RuntimeDir = filepath.Join(t.TempDir(), "absent")
	if m.Available() {
		t.Fatal("no systemd runtime dir")
	}
}

func TestExecRunner(t *testing.T) {
	out, err := ExecRunner(context.Background(), "sh", "-c", "echo hi")
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestFake(t *testing.T) {
	var m Manager = NewFake()
	f := m.(*Fake)
	u := Unit{Job: "j", OnCalendar: "daily"}
	if err := m.Enable(u.Name()); err == nil {
		t.Fatal("enable before install should fail")
	}
	if err := m.Install(u); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(u.Name()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.IsActive(u.Name()); !ok {
		t.Fatal("should be active")
	}
	if err := m.Start(u.Name()); err != nil || len(f.Started) != 1 {
		t.Fatal("start")
	}
	if err := m.Disable(u.Name()); err != nil {
		t.Fatal(err)
	}
	if ok, _ := m.IsActive(u.Name()); ok {
		t.Fatal("still active")
	}
	if err := m.Remove(u.Name()); err != nil || len(f.Installed) != 0 {
		t.Fatal("remove")
	}
	f.FailOn["Start"] = errors.New("x")
	f.FailOn["Install"] = errors.New("x")
	f.FailOn["Enable"] = errors.New("x")
	f.FailOn["Disable"] = errors.New("x")
	f.FailOn["Remove"] = errors.New("x")
	f.FailOn["IsActive"] = errors.New("x")
	if m.Start("a") == nil || m.Install(u) == nil || m.Enable("a") == nil || m.Disable("a") == nil || m.Remove("a") == nil {
		t.Fatal("injected failures not returned")
	}
	if _, err := m.IsActive("a"); err == nil {
		t.Fatal("isactive failure")
	}
	if !m.Available() {
		t.Fatal("available")
	}
	f.Avail = false
	if m.Available() {
		t.Fatal("unavailable")
	}
}
