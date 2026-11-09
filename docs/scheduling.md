# Scheduling

`backup-cli add` installs a systemd service and timer per job:
`backup-cli-<job>.service` (oneshot, `Nice=19`, idle I/O scheduling) and
`backup-cli-<job>.timer` (`Persistent=true`, so a missed run happens at next boot).
The service runs `backup-cli --state-dir <dir> run <job>` using the absolute path
of the binary that ran `add`.

## Schedule specs

| Spec | Meaning |
|---|---|
| `daily,HH:MM` | every day |
| `weekly,<day>,HH:MM` | every week on that day |
| `biweekly,<day>,HH:MM` | every other week (see below) |
| `monthly,<dom>,HH:MM` | day of month 1-28 |
| `oncalendar:<expr>` | any systemd `OnCalendar` expression |

Days are `mon`..`sun` (full names also accepted, case-insensitive). `HH:MM` is
24-hour local time; `24:00` and out-of-range values are rejected. Days 29-31
are rejected for monthly because not every month has them. `oncalendar:` values
must be non-empty, free of control characters, and use only allowed characters;
if `systemd-analyze` is present the expression is also validated with it.

Examples:

```sh
backup-cli add /srv/data /mnt/backup/data-{timestamp} --schedule daily,02:30 --keep 7
backup-cli add /srv/data /mnt/backup/data-{timestamp} --schedule weekly,sun,03:00 --keep 4
backup-cli add /srv/data /mnt/backup/data-{timestamp} --schedule 'oncalendar:Mon..Fri 01:15' --keep 10
```

## Biweekly

systemd has no native "every second week". A biweekly job fires weekly and the
run itself skips when the newest committed backup is under 13 days old. A
skipped run is recorded with state `skipped` (reason `biweekly-gate`) and exits 0.
`run --force` bypasses the gate.

## Starting immediately

`add --now` starts the job once, detached (`systemctl start --no-block`), prints
the run hint and the `tail` command, and returns. Without systemd it runs in the
foreground.

## Without systemd

If systemd is not available, `add` does not schedule anything: it prints the
equivalent cron line, for example

```
30 2 * * * /usr/local/bin/backup-cli run data
```

and exits 4. `oncalendar:` specs cannot be converted to cron, and a message says
so. Use `add --no-schedule` to register the job without a schedule (it is saved
with `scheduled: false`) and drive it from cron yourself.

## Unit directory

Units are written to `/etc/systemd/system`. Override with the global `--unit-dir`
flag (before the command) or `BACKUP_CLI_UNIT_DIR`.

The service unit has `After=local-fs.target remote-fs.target`, so the system
tries to mount the source first. It deliberately does not use `Requires=` or
`RequiresMountsFor=`: with those, a failed mount makes systemd skip the service
as a dependency failure, so backup-cli never runs and the on-failure hook never
fires. With ordering only, the run still starts, the empty-source check refuses
it (exit 4) and the hook alerts.

## Deadline

`--deadline` is stored with the job and resolved at each run start: an absolute
time (RFC3339 or `YYYY-MM-DD HH:MM`) or a duration (`2h30m`) relative to run start.
When the deadline passes, the run stops cleanly with exit 3 and resumes next time.

## Removing

`backup-cli job rm <job>` disables and removes the units and the job file. Backups
on disk stay.
