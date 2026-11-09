package engine

import (
	"context"
	"strconv"

	"github.com/drizki/backup-cli/internal/backupset"
	"github.com/drizki/backup-cli/internal/exitcode"
	"github.com/drizki/backup-cli/internal/hooks"
	"github.com/drizki/backup-cli/internal/manifest"
	"github.com/drizki/backup-cli/internal/state"
)

// afterCommit verifies a sample of the new backup and then runs retention.
// A failed verification fails the run and skips retention.
func (r *run) afterCommit(ctx context.Context) error {
	if r.e.PostCommit != nil {
		r.e.PostCommit(r.finalDir)
	}
	if err := r.verifySample(ctx); err != nil {
		return err
	}
	if r.e.Retention == nil {
		return nil
	}
	all, err := backupset.Scan(r.j)
	if err != nil {
		r.warn("retention skipped: %v", err)
		return nil
	}
	if err := r.e.Retention(ctx, r.j, backupset.OnlyCommitted(all), r.log); err != nil {
		r.warn("retention failed: %v", err)
	}
	return nil
}

func (r *run) samplePct() int {
	if r.req.VerifySample >= 0 {
		return r.req.VerifySample
	}
	return r.j.VerifySamplePct
}

func (r *run) verifySample(ctx context.Context) error {
	pct := r.samplePct()
	if pct <= 0 {
		r.log.Infof("post-commit verification is disabled")
		return nil
	}
	rep, err := manifest.Verify(ctx, r.finalDir, manifest.VerifyOptions{SamplePct: float64(pct), Rand: r.e.Rand})
	if err != nil {
		return exitcode.Failedf("verify %s: %v", r.finalDir, err)
	}
	for _, w := range rep.Warnings {
		r.log.Warnf("verify: %s", w)
	}
	if !rep.OK() {
		r.update(func(s *state.Status) { s.VerifyFailed = true })
		for _, f := range rep.Failures {
			r.log.Errorf("verify: %s", f)
		}
		return exitcode.Failedf("post-commit verification failed (%d problem(s)); the backup %s is kept but flagged", len(rep.Failures), r.finalDir)
	}
	r.log.Infof("verified %d of %d file(s): ok", rep.Checked, rep.Files)
	return nil
}

// runHook runs the job's on-success or on-failure command. Failures are
// logged and never change the run's result.
func (r *run) runHook(ctx context.Context, st string, code int) {
	cmd := r.j.OnFailure
	switch st {
	case state.StateSucceeded:
		cmd = r.j.OnSuccess
	case state.StateSkipped:
		return
	}
	if r.dry || cmd == "" {
		return
	}
	path := r.finalDir
	if st != state.StateSucceeded {
		path = r.st.PartialDir
	}
	env := []string{
		"BACKUP_CLI_JOB=" + r.j.Name,
		"BACKUP_CLI_RUN_ID=" + r.id,
		"BACKUP_CLI_STATUS=" + st,
		"BACKUP_CLI_LOG=" + r.st.Log,
		"BACKUP_CLI_BACKUP_PATH=" + path,
		"BACKUP_CLI_EXIT_CODE=" + strconv.Itoa(code),
	}
	timeout := r.e.HookTimeout
	if timeout <= 0 {
		timeout = hooks.DefaultTimeout
	}
	out, err := hooks.Run(context.WithoutCancel(ctx), cmd, env, timeout)
	if out != "" {
		r.log.Infof("hook output: %s", out)
	}
	if err != nil {
		r.log.Warnf("hook failed: %v", err)
		return
	}
	r.log.Infof("hook finished")
}
