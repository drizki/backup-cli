// Package job defines the job model, its validation and naming helpers.
package job

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Version is the job file schema version.
const Version = 1

// TimestampToken is the placeholder required in a destination template.
const TimestampToken = "{timestamp}"

// TimestampLayout is the UTC layout substituted for {timestamp}.
const TimestampLayout = "20060102T150405Z"

// DefaultKeep, DefaultJobs and DefaultVerifySamplePct are the defaults for new jobs.
const (
	DefaultKeep            = 7
	DefaultJobs            = 4
	DefaultVerifySamplePct = 2
)

var (
	nameRe      = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
	timestampRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
)

// Job is the persisted job definition (jobs/<name>.json).
type Job struct {
	Version         int       `json:"version"`
	Name            string    `json:"name"`
	Source          string    `json:"source"`
	DstTemplate     string    `json:"dst_template"`
	Schedule        string    `json:"schedule"`
	OnCalendar      string    `json:"on_calendar"`
	Biweekly        bool      `json:"biweekly"`
	Keep            int       `json:"keep"`
	Deadline        string    `json:"deadline"`
	OnSuccess       string    `json:"on_success"`
	OnFailure       string    `json:"on_failure"`
	Scheduled       bool      `json:"scheduled"`
	Created         time.Time `json:"created"`
	VerifySamplePct int       `json:"verify_sample_pct"`
	Jobs            int       `json:"jobs"`
}

// ValidName reports whether name is a legal job name.
func ValidName(name string) bool { return nameRe.MatchString(name) }

// Validate checks every field that does not require touching the filesystem.
func (j Job) Validate() error {
	if !ValidName(j.Name) {
		return fmt.Errorf("invalid job name %q: use 1-64 characters from [a-z0-9._-]", j.Name)
	}
	if j.Name == "." || j.Name == ".." {
		return fmt.Errorf("invalid job name %q", j.Name)
	}
	if !filepath.IsAbs(j.Source) {
		return fmt.Errorf("source %q must be an absolute path", j.Source)
	}
	if err := ValidateTemplate(j.DstTemplate); err != nil {
		return err
	}
	if j.Keep < 1 {
		return fmt.Errorf("keep must be >= 1, got %d", j.Keep)
	}
	if j.Jobs < 1 {
		return fmt.Errorf("jobs must be >= 1, got %d", j.Jobs)
	}
	if j.VerifySamplePct < 0 || j.VerifySamplePct > 100 {
		return fmt.Errorf("verify sample must be 0-100, got %d", j.VerifySamplePct)
	}
	if j.Deadline != "" {
		if _, err := ParseDeadline(j.Deadline, time.Now()); err != nil {
			return err
		}
	}
	return nil
}

// ValidateTemplate checks a destination template: absolute, exactly one
// {timestamp} token, located in the final path element (so every backup of a
// job lives directly in one parent directory).
func ValidateTemplate(t string) error {
	if !filepath.IsAbs(t) {
		return fmt.Errorf("destination template %q must be an absolute path", t)
	}
	if strings.ContainsRune(t, 0) || strings.ContainsAny(t, "\n\r") {
		return errors.New("destination template contains control characters")
	}
	if strings.Count(t, TimestampToken) != 1 {
		return fmt.Errorf("destination template %q must contain %s exactly once", t, TimestampToken)
	}
	if strings.Contains(filepath.Dir(t), TimestampToken) {
		return fmt.Errorf("%s must be in the last path element of %q", TimestampToken, t)
	}
	base := filepath.Base(t)
	if strings.HasSuffix(base, ".partial") {
		return errors.New("destination template must not end in .partial")
	}
	return nil
}

// DstFor expands the template for the given instant (UTC).
func (j Job) DstFor(ts time.Time) string {
	return filepath.Clean(strings.Replace(j.DstTemplate, TimestampToken, ts.UTC().Format(TimestampLayout), 1))
}

// DstParent is the directory that holds all backups of this job.
func (j Job) DstParent() string { return filepath.Dir(j.DstTemplate) }

// MatchesName reports whether a directory name looks like a backup produced
// from this job's template (prefix + UTC timestamp + suffix).
func (j Job) MatchesName(name string) bool {
	prefix, suffix, ok := strings.Cut(filepath.Base(j.DstTemplate), TimestampToken)
	if !ok || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	if len(name) < len(prefix)+len(suffix) {
		return false
	}
	return timestampRe.MatchString(name[len(prefix) : len(name)-len(suffix)])
}

// Slug turns an arbitrary string into a valid job name.
func Slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_'
		if ok {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-._")
	if len(out) > 56 { // leave room for a uniquifying suffix
		out = strings.Trim(out[:56], "-._")
	}
	if out == "" {
		out = "job"
	}
	return out
}

// Unique returns base, or base-2, base-3, ... that is not in taken.
func Unique(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		c := fmt.Sprintf("%s-%d", base, i)
		if !taken(c) {
			return c
		}
	}
}

// ParseDeadline resolves a deadline string relative to start. Accepted forms:
// RFC3339, "YYYY-MM-DD HH:MM" (local time) or a positive duration ("2h30m").
func ParseDeadline(s string, start time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("deadline is empty")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		return t, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("deadline duration %q must be positive", s)
		}
		return start.Add(d), nil
	}
	return time.Time{}, fmt.Errorf("invalid deadline %q: use RFC3339, \"YYYY-MM-DD HH:MM\" or a duration like 2h30m", s)
}
