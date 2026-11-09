# Doctor and troubleshooting

`backup-cli doctor` checks the environment and each job. `doctor --json` prints
the same result with a severity per check. Exit code 0 means no failures
(warnings still exit 0); exit 1 means at least one check failed.

## Checks

| Check | Failure or warning means | What to do |
|---|---|---|
| state directory | fail: not writable or not a directory, so runs cannot be recorded; warn: it does not exist yet | fix permissions, or pass `--state-dir` |
| systemd | warn: systemd is not available | use cron; see [scheduling](scheduling.md) |
| zfs block cloning | warn when `zfs_bclone_enabled` is `1`; ok when `0` or when the parameter does not exist | cloning can make copies share blocks below the filesystem; set the module parameter to 0 if you need physically separate copies |
| source readable | fail: the job cannot run | check the path, mount and permissions |
| source writable | warn: the account that runs backups can modify the source | prefer a read-only mount or a read-only account |
| overlap | fail: source and destination overlap (for example a hand-edited job file) | fix the job; `run` would refuse it |
| destination writable | fail: cannot write backups | check mount and permissions |
| destination mount | warn: the destination is on the root filesystem or not in the mount table | check that the backup disk is mounted |
| backups | the number of committed backups | |
| free space | fail: less than `keep + 1` backups of the newest backup's size fit (counting those already stored); ok when there is no backup yet | free space, lower `--keep`, or use a larger disk |
| memory | warn: the newest backup's file count times the measured peak memory per file exceeds the cgroup v2 `memory.max`, so a run may be killed; not reported without a cgroup limit or a backup | split the source into several jobs or raise the limit; see Known limits in [how it works](how-it-works.md) |
| leftover partial | warn: an interrupted run left a `.partial` directory | run the job again to resume it, or remove it with `rm`; doctor never deletes it |
| unrecognized directories | warn: directories matching the job's template that are not committed backups (no marker, bad marker, another job's marker) | they are never pruned or touched; inspect or remove them by hand |
| last run | fail: the last run failed; warn: partial, refused, never run (scheduled jobs), finished more than 40 days ago, or recorded as running although its process is gone | read its log with `backup-cli tail <run-id>` |
| lock | warn: the lock is held but the recorded pid is not running, or a crashed run left a lock file naming a dead pid | the lock file is kept after every run and a free lock is normal; a new run reuses it |
| timer | warn: a scheduled job's timer is not active, or systemd is missing | `systemctl enable --now backup-cli-<job>.timer`, or add the job again |
| version | reports the binary version | include it in bug reports |

Checks that depend on a job are listed once per job.

## Common situations

**Run refused (exit 4).** Read `refuse_reason` in `runs/<run-id>/status.json`.
Typical reasons: lock held (another run is active), overlap between source and
destination, not enough space, a leftover `.partial` from a different job, or a
root file in the source named `MANIFEST.sha256`, `MANIFEST.tsv` or `.backup-cli.json`,
or a source with under half the files of the previous backup (the share may not
be mounted).

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
