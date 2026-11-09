# Commands

```
backup-cli [global flags] <command> [flags] [args]
```

## Global flags

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `--state-dir DIR` | `BACKUP_CLI_STATE_DIR` | `/var/lib/backup-cli` | where jobs, runs and locks live |
| `--verbose` | | off | more output |

## add

Register a job and install its schedule.

```
backup-cli add <source> <dst-template> [flags]
```

`<source>` is an absolute path to an existing directory. `<dst-template>` is an
absolute path that must contain `{timestamp}`, which becomes the UTC start time
(`20060102T150405Z`). Source and destination must not overlap.

| Flag | Meaning |
|---|---|
| `--name NAME` | job name, `[a-z0-9._-]{1,64}`; default is a slug of the source basename, made unique |
| `--schedule SPEC` | `daily,HH:MM`, `weekly,<day>,HH:MM`, `biweekly,<day>,HH:MM`, `monthly,<1-28>,HH:MM`, `oncalendar:<expr>` |
| `--keep N` | number of backups to keep, at least 1 |
| `--deadline VALUE` | stop cleanly at an absolute time (RFC3339 or `YYYY-MM-DD HH:MM`) or after a duration (`2h30m`) |
| `--on-success CMD` | shell command run after a successful run |
| `--on-failure CMD` | shell command run after a failed or partial run |
| `--jobs N` | copy workers (default 4) |
| `--now` | start one run immediately; detached under systemd, foreground otherwise |
| `--no-schedule` | save the job without installing a timer |
| `--unit-dir DIR` | systemd unit directory (env `BACKUP_CLI_UNIT_DIR`, default `/etc/systemd/system`) |

Without systemd, `add` prints the equivalent cron line and exits 4 (unless
`--no-schedule`). See [scheduling](scheduling.md).

## run

```
backup-cli run <job> [flags]
```

| Flag | Meaning |
|---|---|
| `--dry-run` | plan only: print counts and bytes to copy from source, copy from previous, and prune; writes nothing anywhere |
| `--from-source` | ignore the previous backup and read everything from the source |
| `--force` | bypass the biweekly gate |
| `--jobs N` | override the job's worker count |
| `--verify-sample PCT` | post-commit sample verification percent (default from the job, 2; 0 disables) |

## list

```
backup-cli list jobs|runs|backups [--json]
```

Tables for humans, or JSON with `--json`. `list backups` shows committed backups
(valid marker) for every job, with the ids accepted by `rm` and `verify`.

## tail

```
backup-cli tail <run-id>
```

Follows the run log while the run is active and returns when it reaches a final
state. For a finished run it prints the whole log.

## verify

```
backup-cli verify <backup> [--sample PCT]
```

Re-hashes files against the backup's manifest. `<backup>` is a path or a
`<job>/<name>` id. `--sample PCT` (0-100) checks a random percentage; default is all files.
Reports mismatches, missing files, and extra files (extra files are a warning).

## rm

```
backup-cli rm <backup> [--yes] [--dry-run] [--force]
```

Delete one backup through the guarded delete path. `<backup>` is a directory or
a `<job>/<name>` id from `list backups`. A leftover `<name>.partial` directory can
be removed the same way (its journal must name the job).

| Flag | Meaning |
|---|---|
| `--yes` | skip the confirmation prompt (required when stdin is not a terminal) |
| `--dry-run` | run every guard, print what would be deleted, delete nothing |
| `--force` | allow deleting the last good backup of a job; all guards still apply |

The prompt shows the path, the file count and the size (from the backup's
marker). Without a terminal on stdin and without `--yes`, `rm` exits 2 and
deletes nothing. Answer `y` or `yes` to confirm; anything else aborts with exit 0.

`rm` exits 4 (refused) when a guard stops it: the last good backup without
`--force`, a missing or foreign marker, a symlink, a path outside the job's
destination parent, overlap with the source or the state directory, a mount
point, or a job that no longer exists (after `job rm` the guards cannot be
checked, so remove such backups by hand).

## job rm

```
backup-cli job rm <job>
```

Disable and remove the job's timer and service and its job file. Backups on disk
and run history are kept.

## doctor

```
backup-cli doctor [--json]
```

Reads only; never changes or deletes anything. Human output has one line per
check (`ok`, `WARN`, `FAIL`); `--json` prints `{version, ok, failures, warnings,
checks:[{name, job, severity, detail}]}`. Exit 0 when nothing failed (warnings
still exit 0), 1 when at least one check failed. See [doctor](doctor.md).

## version, help

`backup-cli version` prints the version, commit and build date. Release binaries
report their tag (`v1.2.3`); local builds without a tag report `dev`.
`backup-cli help [command]` prints usage.

## Exit codes

| Code | Name | Meaning |
|---|---|---|
| 0 | success | run succeeded, or the biweekly gate skipped it |
| 1 | failed | I/O error, verification failure, unreadable file, failed doctor check |
| 2 | usage | bad flags or arguments, invalid schedule, `rm` without `--yes` on a non-terminal |
| 3 | partial | deadline reached; `.partial` kept and will resume |
| 4 | refused | a safety guard stopped it (including `rm`: last good backup, delete guards): overlap, lock held, no space, unknown leftover directory, name collision, no systemd for `add` |

## Hooks

`--on-success` and `--on-failure` run through `sh -c` with a 5 minute timeout
after the run status is final. Partial runs fire the failure hook. Environment:

| Variable | Value |
|---|---|
| `BACKUP_CLI_JOB` | job name |
| `BACKUP_CLI_RUN_ID` | run id |
| `BACKUP_CLI_STATUS` | final state |
| `BACKUP_CLI_LOG` | path to the run log |
| `BACKUP_CLI_BACKUP_PATH` | the backup directory |
| `BACKUP_CLI_EXIT_CODE` | exit code |

A hook failure is logged and does not change the run result.
