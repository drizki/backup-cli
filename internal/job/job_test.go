package job

import (
	"strings"
	"testing"
	"time"
)

func valid() Job {
	return Job{Version: 1, Name: "docs", Source: "/srv/data", DstTemplate: "/mnt/backup/docs-{timestamp}",
		Keep: 3, Jobs: 4, VerifySamplePct: 2}
}

func TestValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatal(err)
	}
	mut := map[string]func(*Job){
		"name upper":        func(j *Job) { j.Name = "Docs" },
		"name empty":        func(j *Job) { j.Name = "" },
		"name slash":        func(j *Job) { j.Name = "a/b" },
		"name too long":     func(j *Job) { j.Name = strings.Repeat("a", 65) },
		"relative source":   func(j *Job) { j.Source = "data" },
		"no token":          func(j *Job) { j.DstTemplate = "/mnt/backup/docs" },
		"two tokens":        func(j *Job) { j.DstTemplate = "/mnt/{timestamp}-{timestamp}" },
		"token in parent":   func(j *Job) { j.DstTemplate = "/mnt/{timestamp}/docs" },
		"relative template": func(j *Job) { j.DstTemplate = "x-{timestamp}" },
		"partial suffix":    func(j *Job) { j.DstTemplate = "/mnt/x-{timestamp}.partial" },
		"newline":           func(j *Job) { j.DstTemplate = "/mnt/x\n-{timestamp}" },
		"keep 0":            func(j *Job) { j.Keep = 0 },
		"jobs 0":            func(j *Job) { j.Jobs = 0 },
		"pct 101":           func(j *Job) { j.VerifySamplePct = 101 },
		"pct -1":            func(j *Job) { j.VerifySamplePct = -1 },
		"bad deadline":      func(j *Job) { j.Deadline = "soon" },
	}
	for name, f := range mut {
		j := valid()
		f(&j)
		if err := j.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	j := valid()
	j.Deadline = "2h"
	if err := j.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDstAndMatch(t *testing.T) {
	j := valid()
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.FixedZone("x", 3600))
	if got := j.DstFor(ts); got != "/mnt/backup/docs-20260304T040607Z" {
		t.Fatalf("DstFor=%s", got)
	}
	if j.DstParent() != "/mnt/backup" {
		t.Fatal(j.DstParent())
	}
	for name, want := range map[string]bool{
		"docs-20260304T040607Z":         true,
		"docs-20260304T040607Z.partial": false,
		"docs-2026":                     false,
		"docs-":                         false,
		"other-20260304T040607Z":        false,
		"docs-abcdefghTabcdefZ":         false,
	} {
		if j.MatchesName(name) != want {
			t.Errorf("MatchesName(%q)!=%v", name, want)
		}
	}
	j.DstTemplate = "/mnt/backup/{timestamp}-docs"
	if !j.MatchesName("20260304T040607Z-docs") || j.MatchesName("20260304T040607Z") {
		t.Fatal("suffix template mismatch")
	}
}

func TestSlugAndUnique(t *testing.T) {
	cases := map[string]string{
		"My Documents":           "my-documents",
		"data":                   "data",
		"  --x--  ":              "x",
		"Ünïcode/Päth":           "n-code-p-th",
		"":                       "job",
		"///":                    "job",
		"a.b_c-d":                "a.b_c-d",
		strings.Repeat("x", 100): strings.Repeat("x", 56),
	}
	for in, want := range cases {
		got := Slug(in)
		if got != want {
			t.Errorf("Slug(%q)=%q want %q", in, got, want)
		}
		if !ValidName(got) {
			t.Errorf("Slug(%q)=%q invalid", in, got)
		}
	}
	taken := map[string]bool{"data": true, "data-2": true}
	if got := Unique("data", func(s string) bool { return taken[s] }); got != "data-3" {
		t.Fatal(got)
	}
	if got := Unique("fresh", func(s string) bool { return taken[s] }); got != "fresh" {
		t.Fatal(got)
	}
}

func TestParseDeadline(t *testing.T) {
	start := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	got, err := ParseDeadline("2h30m", start)
	if err != nil || !got.Equal(start.Add(150*time.Minute)) {
		t.Fatalf("duration: %v %v", got, err)
	}
	got, err = ParseDeadline("2026-05-06T07:08:09Z", start)
	if err != nil || !got.Equal(time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)) {
		t.Fatalf("rfc3339: %v %v", got, err)
	}
	got, err = ParseDeadline("2026-05-06 07:08", start)
	if err != nil || got.Year() != 2026 || got.Hour() != 7 || got.Minute() != 8 || got.Location() != time.Local {
		t.Fatalf("local: %v %v", got, err)
	}
	for _, bad := range []string{"", "  ", "0s", "-1h", "tomorrow", "2026-13-01 00:00", "25"} {
		if _, err := ParseDeadline(bad, start); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestDstForCleansTheTemplate(t *testing.T) {
	j := valid()
	j.DstTemplate = "/mnt//backup/./docs-{timestamp}"
	ts := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	if got := j.DstFor(ts); got != "/mnt/backup/docs-20260304T050607Z" {
		t.Fatalf("DstFor=%s", got)
	}
	if j.DstParent() != "/mnt/backup" || !j.MatchesName("docs-20260304T050607Z") {
		t.Fatalf("parent %s", j.DstParent())
	}
}
