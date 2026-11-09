# Safety model

backup-cli's job is to keep your data safe, so its own failure modes matter more
than its features. This page lists what it will and will not do.

## The source is read-only

Source files are opened `O_RDONLY`. The tool never writes, renames, chmods or
deletes anything under the source. `--dry-run` goes further and writes nothing
in the source, the destination or the state directory (no run directory, no log).

## Backups are independent

No hardlinks, reflinks or block sharing between backups. Removing or corrupting
one backup cannot affect another.

## Commit protocol

Work happens in `<dst>.partial`. The order at the end is: remove anything the
plan does not contain, manifests, marker,
fsync of files and directory, atomic rename, fsync of the parent. Only a
renamed directory with a valid marker is a committed backup.

## Single deletion path

Every deletion in the code base goes through one package (`safedelete`), and a
repository test fails if deletion calls appear anywhere else. Before removing a
backup it checks that:

- the directory contains a valid marker whose job matches the job being acted on;
- the target is an absolute path with no `..` element, is directly inside the
  job's destination parent spelled exactly as the job spells it (the parent
  itself, other locations and paths through a symlinked alias are refused), and
  its name matches the job's destination template;
- the target does not equal, contain, or sit inside the job's source, including
  through symlinks and by inode (which also catches bind-style aliases);
- the target is not `/`, not the state directory or anything inside it (or
  containing it), and not a mount point or the parent of one;
- the target is not a symlink (symlinks are refused, never followed);
- the target is the same directory right before removal: it is opened relative
  to a handle on its parent and re-checked (type and identity), and the removal
  is refused if it changed after the guards ran.

Deleting a backup removes its marker first (and syncs the directory), after
making read-only directories owner-writable, so a deletion that is cut short
never leaves a tree that still looks committed.

A run also removes, inside its own proven `.partial` directory only, entries
that are not part of its plan (leftovers of an interrupted run, objects of the
wrong type). That removal is the `SweepPartial` function of the same package; it
works through a handle on the directory, never follows symlinks and leaves the
journal alone.

`.partial` directories can be removed only through a separate function that
checks the `.partial` suffix, a journal belonging to the same job, and the same
location rule. A refusal is reported as exit code 4 and leaves the tree untouched.

The repository test `internal/safedelete/guard_test.go` parses every non-test Go
file and fails on any removal call or spawned `rm` outside `safedelete`, with a
short allowlist (each entry justified in the test): the job definition file and
the atomic-write temp file in `state`, the unit files this tool generated in
`systemd`, and the run's own journal file in `journal`.

## Retention is conservative

Pruning runs only after a run committed and verified successfully. It never
runs after a failed, partial, refused, skipped or dry run. It keeps the newest N
committed backups by marker creation time, never removes the backup the run just
made, and never touches unknown directories in the destination parent. Each
removal goes through the guards above; one refusal is logged as a warning and
the other old backups are still considered. `keep` must be at least 1, so the last
good backup is never pruned. `rm` refuses to delete the last good backup unless
`--force` is given, and `--force` still obeys every guard above.

## Overlap and concurrency

`add` and `run` refuse a source and destination that overlap in either
direction, resolved through symlinks, and also when a job file was edited by
hand into an overlapping state. A per-job file lock refuses a second concurrent
run (exit 4). `rm` takes the same lock, so it cannot delete a backup that a
running job is writing or reading.

## Unmounted or emptied source

An unmounted share is an empty, readable directory. After walking the source
and before any sweep, copy, commit or retention, a run refuses (exit 4) when
the source holds fewer than half the files of the newest committed backup or of
the partial being resumed, so an empty source never replaces good backups. The
refusal fires the on-failure hook. `run --force` overrides the check. The
generated systemd service is ordered after `local-fs.target` and
`remote-fs.target`, so mounts are attempted first. A smaller drop (a
mass deletion or in-place encryption that keeps the file count) is not
detected; use a larger `keep` or filesystem snapshots of the backup dataset.

## Space

Before copying, the free-space check refuses with the required and available
sizes rather than filling the disk.

## Hooks

`--on-success` and `--on-failure` commands run after the status is finalized,
with a 5 minute timeout. A failing hook is logged and never changes the run result.

## What it does not do

- No encryption or compression of backups.
- No protection against a compromised host: whoever can write the destination
  can alter backups. Use separate credentials, a read-only mount after the run,
  or an offsite copy for that.
- No deduplication across backups; each backup uses full space.
