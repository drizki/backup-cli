package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/systemd"
)

func TestAddScheduled(t *testing.T) {
	e := newEnv(t)
	e.mustRun("--unit-dir", "/custom/units", "add", e.src, e.tmpl("data"), "--schedule", "weekly,sun,03:00",
		"--keep", "3", "--deadline", "2h", "--on-success", "echo ok", "--on-failure", "echo bad", "--jobs", "2", "--verify-sample", "5")
	if e.units != "/custom/units" {
		t.Fatalf("unit dir not passed to manager: %q", e.units)
	}
	j, err := e.store().LoadJob("data")
	if err != nil {
		t.Fatal(err)
	}
	if !j.Scheduled || j.Biweekly || j.Schedule != "weekly,sun,03:00" {
		t.Fatalf("schedule fields: %+v", j)
	}
	if j.Keep != 3 || j.Jobs != 2 || j.VerifySamplePct != 5 || j.Deadline != "2h" {
		t.Fatalf("numeric fields: %+v", j)
	}
	if j.OnSuccess != "echo ok" || j.OnFailure != "echo bad" {
		t.Fatalf("hooks: %+v", j)
	}
	checkInstalledUnit(t, e)
}

func checkInstalledUnit(t *testing.T, e *env) {
	t.Helper()
	u, ok := e.fake.Installed["backup-cli-data"]
	if !ok || !e.fake.Enabled["backup-cli-data"] {
		t.Fatal("unit not installed and enabled")
	}
	if u.OnCalendar != "Sun *-*-* 03:00:00" || u.Binary != "/usr/local/bin/backup-cli" || u.StateDir != e.state {
		t.Fatalf("unit: %+v", u)
	}
	if len(e.fake.Started) != 0 {
		t.Fatal("started without --now")
	}
}

func TestAddEnvUnitAndStateDir(t *testing.T) {
	e := newEnv(t)
	e.deps.Getenv = func(k string) string {
		return map[string]string{"BACKUP_CLI_UNIT_DIR": "/env/units", "BACKUP_CLI_STATE_DIR": e.state}[k]
	}
	e.out.Reset()
	if code := Run(t.Context(), []string{"add", e.src, e.tmpl("x"), "--schedule", "daily,01:00"}, e.deps); code != 0 {
		t.Fatalf("exit %d %s", code, e.errb.String())
	}
	if e.units != "/env/units" || !e.jobExists("data") {
		t.Fatalf("env not honoured: %s", e.units)
	}
}

func TestAddBiweeklyAndDefaultName(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("a"), "--schedule", "biweekly,sat,04:00")
	j, err := e.store().LoadJob("data") // slug of the source basename
	if err != nil || !j.Biweekly {
		t.Fatalf("%+v %v", j, err)
	}
	e.mustRun("add", e.src, e.tmpl("b"), "--schedule", "daily,01:00")
	if !e.jobExists("data-2") {
		t.Fatal("name was not uniquified")
	}
}

func TestAddNoSystemdRefusesWithCron(t *testing.T) {
	e := newEnv(t)
	e.fake.Avail = false
	e.expectCode(exitcode.Refused, "add", e.src, e.tmpl("data"), "--schedule", "daily,02:30")
	if !strings.Contains(e.out.String(), "30 2 * * * /usr/local/bin/backup-cli --state-dir "+e.state+" run data") {
		t.Fatalf("cron line missing: %q", e.out.String())
	}
	if e.jobExists("data") || len(e.fake.Installed) != 0 || len(e.fake.Enabled) != 0 || len(e.fake.Calls) != 0 {
		t.Fatal("something was saved or enabled")
	}

	e.expectCode(exitcode.Refused, "add", e.src, e.tmpl("data"), "--schedule", "oncalendar:hourly")
	if !strings.Contains(e.out.String(), "cannot be derived") || e.jobExists("data") {
		t.Fatalf("oncalendar message: %q", e.out.String())
	}

	e.expectCode(exitcode.Refused, "add", e.src, e.tmpl("data"), "--schedule", "biweekly,mon,01:00")
	if !strings.Contains(e.out.String(), "13 days") {
		t.Fatalf("biweekly note missing: %q", e.out.String())
	}

	// A path with spaces is shell-quoted in the cron line.
	e.deps.Executable = func() (string, error) { return "/opt/my tools/backup-cli", nil }
	e.expectCode(exitcode.Refused, "add", e.src, e.tmpl("data"), "--schedule", "monthly,5,06:00")
	if !strings.Contains(e.out.String(), "'/opt/my tools/backup-cli'") {
		t.Fatalf("quoting: %q", e.out.String())
	}
	e.deps.Executable = func() (string, error) { return "", errors.New("x") }
	e.expectCode(exitcode.Refused, "add", e.src, e.tmpl("data"), "--schedule", "daily,01:00")
	if !strings.Contains(e.out.String(), "/path/to/backup-cli") {
		t.Fatalf("fallback path: %q", e.out.String())
	}
}

func TestAddNoSchedule(t *testing.T) {
	e := newEnv(t)
	e.fake.Avail = false
	e.mustRun("add", e.src, e.tmpl("data"), "--no-schedule")
	j, err := e.store().LoadJob("data")
	if err != nil || j.Scheduled || j.Schedule != "" {
		t.Fatalf("%+v %v", j, err)
	}
	if len(e.fake.Calls) != 0 {
		t.Fatalf("manager touched: %v", e.fake.Calls)
	}
	// --no-schedule still validates a given schedule and records it, unscheduled.
	e.mustRun("add", e.src, e.tmpl("b"), "--name", "kept", "--no-schedule", "--schedule", "daily,01:00")
	if j, _ := e.store().LoadJob("kept"); j.Scheduled || j.Schedule != "daily,01:00" {
		t.Fatalf("%+v", j)
	}
	e.expectCode(exitcode.Usage, "add", e.src, e.tmpl("c"), "--no-schedule", "--schedule", "daily,99:00")
}

func TestAddUsageErrors(t *testing.T) {
	e := newEnv(t)
	file := filepath.Join(filepath.Dir(e.src), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ok := e.tmpl("data")
	cases := map[string][]string{
		"no schedule":         {"add", e.src, ok},
		"template no token":   {"add", e.src, filepath.Join(e.dstP, "plain"), "--schedule", "daily,01:00"},
		"relative template":   {"add", e.src, "bk/x-{timestamp}", "--schedule", "daily,01:00"},
		"relative source":     {"add", "data", ok, "--schedule", "daily,01:00"},
		"missing source":      {"add", filepath.Join(e.src, "nope"), ok, "--schedule", "daily,01:00"},
		"source is file":      {"add", file, ok, "--schedule", "daily,01:00"},
		"keep zero":           {"add", e.src, ok, "--schedule", "daily,01:00", "--keep", "0"},
		"jobs zero":           {"add", e.src, ok, "--schedule", "daily,01:00", "--jobs", "0"},
		"verify sample 101":   {"add", e.src, ok, "--schedule", "daily,01:00", "--verify-sample", "101"},
		"bad deadline":        {"add", e.src, ok, "--schedule", "daily,01:00", "--deadline", "whenever"},
		"bad schedule":        {"add", e.src, ok, "--schedule", "monthly,31,01:00"},
		"bad name":            {"add", e.src, ok, "--schedule", "daily,01:00", "--name", "Bad Name"},
		"too few args":        {"add", e.src, "--schedule", "daily,01:00"},
		"unknown flag":        {"add", e.src, ok, "--bogus"},
		"flag missing value":  {"add", e.src, ok, "--keep"},
		"invalid keep syntax": {"add", e.src, ok, "--keep", "x"},
	}
	for name, args := range cases {
		if code := e.run(args...); code != exitcode.Usage {
			t.Errorf("%s: exit %d want 2 (stderr %s)", name, code, e.errb.String())
		}
	}
	if len(e.fake.Calls) != 0 {
		t.Fatalf("manager touched on usage errors: %v", e.fake.Calls)
	}
}

func TestAddDuplicateName(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("one"), "--schedule", "daily,01:00", "--name", "dup")
	e.expectCode(exitcode.Usage, "add", e.src, e.tmpl("two"), "--schedule", "daily,02:00", "--name", "dup")
	if !strings.Contains(e.errb.String(), "already exists") {
		t.Fatal(e.errb.String())
	}
}

func TestAddRefusesADestinationUsedByAnotherJob(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("one"), "--no-schedule", "--name", "one")
	same := filepath.Join(e.dstP, "x", "..", "one-{timestamp}")
	e.expectCode(exitcode.Refused, "add", e.src, same, "--no-schedule", "--name", "two")
	if !strings.Contains(e.errb.String(), `used by job "one"`) {
		t.Fatalf("unclear message: %s", e.errb.String())
	}
	if _, err := e.store().LoadJob("two"); err == nil {
		t.Fatal("the second job was stored")
	}
	e.mustRun("add", e.src, e.tmpl("other"), "--no-schedule", "--name", "three")
}

func TestAddDeadlineAcceptsDurationsOnly(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("a"), "--no-schedule", "--name", "a", "--deadline", "2h30m")
	if j, _ := e.store().LoadJob("a"); j.Deadline != "2h30m" {
		t.Fatalf("deadline stored as %q", j.Deadline)
	}
	for _, d := range []string{"2030-01-02T03:04:05Z", "2026-11-01 06:00", "0s", "-1h"} {
		e.expectCode(exitcode.Usage, "add", e.src, e.tmpl("b"), "--no-schedule", "--name", "b", "--deadline", d)
		if !strings.Contains(e.errb.String(), "positive duration") {
			t.Errorf("%q: unclear message: %s", d, e.errb.String())
		}
	}
	if _, err := e.store().LoadJob("b"); err == nil {
		t.Fatal("a job with a rejected deadline was stored")
	}
}

func TestAddOverlapRefused(t *testing.T) {
	e := newEnv(t)
	// destination inside the source
	e.expectCode(exitcode.Refused, "add", e.src, filepath.Join(e.src, "bk", "x-{timestamp}"), "--no-schedule")
	// source inside an existing backup directory of the job
	inside := filepath.Join(e.dstP, "x-20260101T000000Z", "deep")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "add", inside, e.tmpl("x"), "--no-schedule")
	// destination reached through a symlink into the source
	link := filepath.Join(e.dstP, "link")
	if err := os.Symlink(e.src, link); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Refused, "add", e.src, filepath.Join(link, "x-{timestamp}"), "--no-schedule")
	if len(e.fake.Calls) != 0 || e.jobExists("data") {
		t.Fatal("overlap refusal had side effects")
	}
}

func TestAddNowDetached(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("data"), "--schedule", "daily,01:00", "--now")
	if len(e.fake.Started) != 1 || e.fake.Started[0] != "backup-cli-data" {
		t.Fatalf("started: %v", e.fake.Started)
	}
	if len(e.calls) != 0 {
		t.Fatal("engine ran in foreground despite systemd")
	}
	out := e.out.String()
	if !strings.Contains(out, "backup-cli-data.service") || !strings.Contains(out, "tail <run-id>") {
		t.Fatalf("hint missing: %q", out)
	}
	e.fake.FailOn["Start"] = errors.New("boom")
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("b"), "--name", "b", "--schedule", "daily,01:00", "--now")
}

func TestAddNowForeground(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.tmpl("data"), "--no-schedule", "--now")
	if len(e.calls) != 1 || e.calls[0].Job.Name != "data" || e.calls[0].VerifySample != -1 || e.calls[0].Store.Dir != e.state {
		t.Fatalf("engine calls: %+v", e.calls)
	}
	if len(e.fake.Started) != 0 {
		t.Fatal("service started for unscheduled job")
	}
	// engine failure propagates its exit code
	e.deps.Engine = func(_ context.Context, _ EngineRequest) error {
		return exitcode.With(exitcode.Partial, errors.New("deadline"))
	}
	e.expectCode(exitcode.Partial, "add", e.src, e.tmpl("b"), "--name", "b", "--no-schedule", "--now")
	// no engine registered
	e.deps.Engine = nil
	e.expectCode(exitcode.Usage, "add", e.src, e.tmpl("c"), "--name", "c", "--no-schedule", "--now")
}

func TestAddRollbackOnFailures(t *testing.T) {
	e := newEnv(t)
	e.fake.FailOn["Install"] = errors.New("no")
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--schedule", "daily,01:00")
	if e.jobExists("data") {
		t.Fatal("job saved after install failure")
	}
	delete(e.fake.FailOn, "Install")
	e.fake.FailOn["Enable"] = errors.New("no")
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--schedule", "daily,01:00")
	if e.jobExists("data") || len(e.fake.Installed) != 0 {
		t.Fatal("units left behind after enable failure")
	}
	delete(e.fake.FailOn, "Enable")
	// Name probing hits a non-"not found" error: reported as a failure.
	jobsDir := filepath.Join(e.state, "jobs")
	if err := os.Remove(jobsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jobsDir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--schedule", "daily,01:00")
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--name", "n", "--no-schedule")
	if len(e.fake.Installed) != 0 {
		t.Fatal("units installed despite failure")
	}
	if err := os.Remove(jobsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(jobsDir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAddSaveFailureRollsBackTimer(t *testing.T) {
	if os.Geteuid() == 0 {
		return // permissions do not bind root; the scenario cannot be produced
	}
	e := newEnv(t)
	if err := e.store().Ensure(); err != nil {
		t.Fatal(err)
	}
	jobsDir := filepath.Join(e.state, "jobs")
	if err := os.Chmod(jobsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(jobsDir, 0o755) }()
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--schedule", "daily,01:00")
	if len(e.fake.Installed) != 0 || len(e.fake.Enabled) != 0 {
		t.Fatal("timer not rolled back")
	}
	if !contains(e.fake.Calls, "Disable backup-cli-data") || !contains(e.fake.Calls, "Remove backup-cli-data") {
		t.Fatalf("rollback calls: %v", e.fake.Calls)
	}
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--no-schedule")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestAddExecutableError(t *testing.T) {
	e := newEnv(t)
	e.deps.Executable = func() (string, error) { return "", errors.New("no exe") }
	e.expectCode(exitcode.Failed, "add", e.src, e.tmpl("data"), "--schedule", "daily,01:00")
}

func TestAddAnalyzeHook(t *testing.T) {
	e := newEnv(t)
	e.deps.Analyze = func(string) error { return errors.New("bad calendar") }
	e.expectCode(exitcode.Usage, "add", e.src, e.tmpl("data"), "--schedule", "oncalendar:hourly")
	e.deps.Analyze = nil
	e.mustRun("add", e.src, e.tmpl("data"), "--schedule", "oncalendar:hourly")
	if e.fake.Installed["backup-cli-data"].OnCalendar != "hourly" {
		t.Fatal("oncalendar not applied")
	}
}

var _ systemd.Manager = (*systemd.Fake)(nil)

func TestAddCleansTheDestinationTemplate(t *testing.T) {
	e := newEnv(t)
	e.mustRun("add", e.src, e.dstP+"//./data-{timestamp}", "--no-schedule")
	j, err := e.store().LoadJob("data")
	if err != nil || j.DstTemplate != filepath.Join(e.dstP, "data-{timestamp}") {
		t.Fatalf("template %q, %v", j.DstTemplate, err)
	}
}
