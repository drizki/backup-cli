# State layout

State lives in the directory given by `--state-dir` or `BACKUP_CLI_STATE_DIR`
(default `/var/lib/backup-cli`). Everything is plain JSON or text, readable with
`cat` and `jq`. Directories are mode 0755, files 0644. JSON files are written
atomically (temp file, fsync, rename).

```
<state-dir>/
  jobs/<job>.json
  runs/<run-id>/status.json
  runs/<run-id>/run.log
  locks/<job>.lock
```

## Job file

```json
{
  "version": 1,
  "name": "data",
  "source": "/srv/data",
  "dst_template": "/mnt/backup/data-{timestamp}",
  "schedule": "daily,02:30",
  "on_calendar": "*-*-* 02:30:00",
  "biweekly": false,
  "keep": 7,
  "deadline": "",
  "on_success": "",
  "on_failure": "",
  "scheduled": true,
  "created": "2026-01-01T00:00:00Z",
  "verify_sample_pct": 2
}
```

`{timestamp}` expands to the UTC start time, for example `20260101T023000Z`.
`schedule` is stored as typed; `deadline` is stored as given and resolved at run start.

## Run id

`<UTC yyyymmddThhmmssZ>-<job>-<4 hex>`, so ids sort chronologically.

## Status file

```json
{
  "run_id": "20260101T023000Z-data-a1b2",
  "job": "data",
  "state": "succeeded",
  "started": "2026-01-01T02:30:00Z",
  "finished": "2026-01-01T02:41:12Z",
  "pid": 1234,
  "tool_version": "v1.0.0",
  "source": "/srv/data",
  "dest": "/mnt/backup/data-20260101T023000Z",
  "partial_dir": "/mnt/backup/data-20260101T023000Z.partial",
  "final_dir": "/mnt/backup/data-20260101T023000Z",
  "files_total": 1000,
  "files_copied_from_source": 12,
  "files_copied_from_prev": 988,
  "bytes_total": 0,
  "bytes_from_source": 0,
  "warnings": [],
  "warnings_total": 0,
  "errors": [],
  "exit_code": 0,
  "refuse_reason": "",
  "dry_run": false,
  "log": "runs/20260101T023000Z-data-a1b2/run.log"
}
```

`warnings` holds the first 100 warnings only; `warnings_total` counts all of them
and `run.log` has every one.

`state` is one of `running`, `succeeded`, `partial`, `failed`, `refused`,
`skipped`. The file is rewritten at start, on every state change, and on a 5
second progress tick while running. `run.log` has timestamped lines; under
systemd the same lines also go to stdout (and the journal).

## Backup layout

```
<dst>/
  <your files as in the source>
  MANIFEST.sha256
  MANIFEST.tsv
  .backup-cli.json
```

`MANIFEST.sha256`: `<hex>  ./relative/path`, compatible with `sha256sum -c`. Names
with newlines, carriage returns or backslashes use GNU escaping (line starts with
`\`, with `\\`, `\n` and `\r` escapes).

`MANIFEST.tsv`: a header and one row per entry:
`path`, `type`, `size`, `mtime_ns`, `mode` (octal), `uid`, `gid`, `sha256`,
`link_target`. Tab, newline, `%` and control characters in paths and link targets
are percent-escaped. Directories and symlinks are included with sha256 `-`.

`.backup-cli.json` (the marker, written last):

```json
{
  "format": 1,
  "job": "data",
  "run_id": "20260101T023000Z-data-a1b2",
  "tool_version": "v1.0.0",
  "created": "2026-01-01T02:41:12Z",
  "started": "2026-01-01T02:30:00Z",
  "source": "/srv/data",
  "file_count": 1000,
  "bytes": 0,
  "manifest_sha256": "<sha256 of MANIFEST.tsv>"
}
```

## Partial directory

`<dst>.partial/` holds the in-progress copy plus `.backup-cli-partial.jsonl`, an
append-only journal: a header per session (job, run id, start time), one entry
per completed file (size, mtime, sha256) appended after the file is fsynced, and
a `clean-stop` record when a session ended by deadline or signal.
