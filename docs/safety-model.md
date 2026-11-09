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

Work happens in `<dst>.partial`. The order at the end is: manifests, marker,
fsync of files and directory, atomic rename, fsync of the parent. Only a
renamed directory with a valid marker is a committed backup.

## Single deletion path

Every deletion in the code base goes through one package (`safedelete`), and a
repository test fails if deletion calls appear anywhere else. Before removing a
backup it checks that:

- the directory contains a valid marker whose job matches the job being acted on;
- the target is strictly inside the job's destination parent (not the parent
  itself, no `..` escapes, no symlinked components);
- the target does not equal, contain, or sit inside the job's source, including
  through symlinks;
- the target is not `/`, not the state directory or anything inside it, and not a
  mount point;
- the target is not a symlink (symlinks are refused, never followed);
- the target type did not change between the check and the removal.

`.partial` directories can be removed only through a separate function that
checks the `.partial` suffix, a journal belonging to the same job, and the same
location rule.

## Retention is conservative

Pruning runs only after a run committed and verified successfully. It never
runs after a failed, partial, refused, skipped or dry run. It keeps the newest N
committed backups by marker creation time, and never touches unknown
directories in the destination parent. `keep` must be at least 1, so the last
good backup is never pruned. `rm` refuses to delete the last good backup unless
`--force` is given, and `--force` still obeys every guard above.

## Overlap and concurrency

`add` and `run` refuse a source and destination that overlap in either
direction, resolved through symlinks, and also when a job file was edited by
hand into an overlapping state. A per-job file lock refuses a second concurrent
run (exit 4).

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
