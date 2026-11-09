package cli

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/job"
	"github.com/drizki/backup-cli/internal/schedule"
	"github.com/drizki/backup-cli/internal/state"
)

// buildJob validates the add arguments and assembles the job. Every failure
// is a usage error (exit 2).
func (a *app) buildJob(store state.Store, src, dst string, o addOpts) (job.Job, schedule.Result, error) {
	var sched schedule.Result
	src, err := checkSource(src)
	if err != nil {
		return job.Job{}, sched, err
	}
	if err := checkAddFlags(dst, o); err != nil {
		return job.Job{}, sched, err
	}
	if sched, err = a.parseSchedule(o); err != nil {
		return job.Job{}, sched, err
	}
	name, err := chooseName(store, src, o.name)
	if err != nil {
		return job.Job{}, sched, err
	}
	j := job.Job{
		Version:         job.Version,
		Name:            name,
		Source:          src,
		DstTemplate:     filepath.Clean(dst),
		Schedule:        sched.Spec,
		OnCalendar:      sched.OnCalendar,
		Biweekly:        sched.BiweeklyGate,
		Keep:            o.keep,
		Deadline:        o.deadline,
		OnSuccess:       o.onSuccess,
		OnFailure:       o.onFailure,
		Scheduled:       !o.noSchedule,
		Created:         time.Now().UTC().Truncate(time.Second),
		VerifySamplePct: o.verifySample,
		Jobs:            o.jobs,
	}
	if err := j.Validate(); err != nil {
		return job.Job{}, sched, exitcode.With(exitcode.Usage, err)
	}
	return j, sched, nil
}

func checkSource(src string) (string, error) {
	if !filepath.IsAbs(src) {
		return "", exitcode.Usagef("source %q must be an absolute path", src)
	}
	src = filepath.Clean(src)
	fi, err := os.Stat(src)
	if err != nil {
		return "", exitcode.Usagef("source: %v", err)
	}
	if !fi.IsDir() {
		return "", exitcode.Usagef("source %s is not a directory", src)
	}
	return src, nil
}

func checkAddFlags(dst string, o addOpts) error {
	if err := job.ValidateTemplate(dst); err != nil {
		return exitcode.With(exitcode.Usage, err)
	}
	if o.keep < 1 {
		return exitcode.Usagef("--keep must be >= 1, got %d", o.keep)
	}
	if o.jobs < 1 {
		return exitcode.Usagef("--jobs must be >= 1, got %d", o.jobs)
	}
	if o.verifySample < 0 || o.verifySample > 100 {
		return exitcode.Usagef("--verify-sample must be 0-100, got %d", o.verifySample)
	}
	if o.deadline == "" {
		return nil
	}
	if _, err := job.ParseDeadline(o.deadline, time.Now()); err != nil {
		return exitcode.With(exitcode.Usage, err)
	}
	return nil
}

func (a *app) parseSchedule(o addOpts) (schedule.Result, error) {
	if o.schedule == "" {
		if !o.noSchedule {
			return schedule.Result{}, exitcode.Usagef("--schedule is required (or pass --no-schedule)")
		}
		return schedule.Result{}, nil
	}
	r, err := schedule.Parse(o.schedule, schedule.Options{Analyze: a.d.Analyze})
	if err != nil {
		return r, exitcode.With(exitcode.Usage, err)
	}
	return r, nil
}

func chooseName(store state.Store, src, want string) (string, error) {
	var lookupErr error
	taken := func(n string) bool {
		_, err := store.LoadJob(n)
		if err == nil {
			return true
		}
		if !errors.Is(err, state.ErrNotFound) {
			lookupErr = err
			return false // stop probing; the error is reported below
		}
		return false
	}
	name := want
	switch {
	case want == "":
		name = job.Unique(job.Slug(filepath.Base(src)), taken)
	case !job.ValidName(want):
		return "", exitcode.Usagef("invalid job name %q: use 1-64 characters from [a-z0-9._-]", want)
	case taken(want):
		return "", exitcode.Usagef("job %q already exists", want)
	}
	if lookupErr != nil {
		return "", exitcode.With(exitcode.Failed, lookupErr)
	}
	return name, nil
}
