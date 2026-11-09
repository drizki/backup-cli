# Doctor and troubleshooting

`backup-cli doctor` checks the environment and each job. `doctor --json` prints
the same result with a severity per check. Exit code 0 means no failures
(warnings still exit 0); exit 1 means at least one check failed.

## Checks

| Check | Failure or warning means | What to do |
|---|---|---|
| State directory writable | cannot record runs | fix permissions, or pass `--state-dir` |
| Source readable | the job cannot run | check the path, mount and permissions |
| Source writable | warning: the account that runs backups can modify the source | prefer a read-only mount or a read-only account |
| Destination writable | cannot write backups | check mount and permissions |
| Free space | less than `keep + 1` backups fit | free space, lower `--keep`, or use a larger disk |
| ZFS block cloning | warning when `zfs_bclone_enabled` is on | cloning can make copies share blocks below the filesystem; set the module parameter to 0 if you need physically separate copies |
| systemd and timers | systemd missing, or a job's timer not enabled | install systemd, or use cron; re-run `add` |
| Last run | last run failed, partial or is old | read its log with `backup-cli tail <run-id>` |
| Leftover `.partial` | an interrupted run left a directory | run the job again to resume it; doctor never deletes it |
| Stale locks | lock file present but not held | safe to ignore; a new run reuses it |
| Version | reports the binary version | include it in bug reports |

## Common situations

**Run refused (exit 4).** Read `refuse_reason` in `runs/<run-id>/status.json`.
Typical reasons: lock held (another run is active), overlap between source and
destination, not enough space, a leftover `.partial` from a different job, or a
root file in the source named `MANIFEST.sha256`, `MANIFEST.tsv` or `.backup-cli.json`.

**Run partial (exit 3).** The deadline hit. Nothing was lost; run the job again
and it resumes.

**Run failed (exit 1).** The log shows the first error. An unreadable source file
fails the run and keeps the `.partial` so nothing is redone. Fix the cause and re-run.

**Bitrot message in a log.** A file in the previous backup did not match its
recorded hash. The file was re-copied from the source, so the new backup is
correct. Consider checking the destination disk.

**Biweekly job did nothing.** State `skipped` with reason `biweekly-gate` is
expected when the last backup is under 13 days old. Use `run --force`.

**No timer after `add`.** On a host without systemd, `add` prints a cron line and
exits 4; see [scheduling](scheduling.md).
