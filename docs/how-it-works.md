# How it works

backup-cli produces a complete copy of a source directory on every run. It
avoids reading unchanged data from the source by reusing the previous backup
as a local cache.

## Run lifecycle

1. Load the job and resolve its deadline. Create the run directory and write
   `status.json` with state `running` (not for `--dry-run`).
2. Take the per-job lock. If another run holds it, refuse (exit 4).
3. Biweekly gate: for `biweekly` jobs, if the newest committed backup is less
   than 13 days old, record the run as `skipped` and exit 0. `--force` bypasses it.
4. Guards: source is a readable directory; source and destination do not
   overlap; the destination parent exists and is writable; the source has no
   root file named like a manifest file; enough free space (about the larger of
   the last backup and the current source size, times 1.05, plus 64 MiB).
5. Find the previous committed backup (newest with a valid marker).
   `--from-source` ignores it.
6. A leftover `<dst>.partial` of the same job is resumed. One that belongs to
   another job or has no matching journal is never touched; the run refuses.
   A partial that holds nothing (a run killed right after creating it, so the
   journal is missing, empty or cut off) is the job's own and is resumed.
7. Walk the source (metadata only) and compare with the previous manifest by
   path, size and mtime. Classify each entry as unchanged, new or changed, or
   removed. A file whose mtime is at or after the previous run's start (minus a
   2 second margin) counts as changed, because a write in the same timestamp
   tick as the copy would not change its size or mtime. A file or directory that
   disappears while the tree is listed is left out with a warning.
8. Copy with a small worker pool (default 4, `--jobs`):
   - unchanged: read from the previous backup, hash while writing, compare with
     the recorded hash. A mismatch is logged as bitrot and the file is
     re-copied from the source.
   - new or changed: read from the source, SHA-256 computed in the same pass.
   - the source file is stat'ed before and after; if it changed during the
     copy, it is retried once, then recorded with a warning.
   - a file that disappears (or is replaced by a directory or other object)
     between the listing and its copy is dropped from the plan and the
     manifests with a warning; the run continues. Permission and I/O errors
     still fail the run.
   - each file is fsynced, then mtime, mode and (as root) uid/gid are applied;
     directories are fsynced and their mtimes set last.
9. Remove from the job's own `.partial` anything the plan does not contain,
   then write `MANIFEST.sha256`, `MANIFEST.tsv` and finally the marker
   `.backup-cli.json` (which records when the run started); fsync; rename
   `<dst>.partial` to `<dst>`; fsync the parent.
10. Sample verification: re-hash a random sample of files (default 2%, at least
    one; `--verify-sample PCT`, 0 disables). A failure marks the run `failed`.
11. Only after a successful run: prune backups beyond `--keep` through the
    guarded delete path. Then hooks run.
12. Release the lock and exit.

## Why a backup is trustworthy

A directory counts as a committed backup only if it was renamed from
`.partial` and contains a valid marker written last. Anything else (a `.partial`
directory, a directory with no marker, a marker that is not valid JSON) is
ignored by listing, retention and verification, and never deleted automatically.

## Interruption and resume

A journal in `<dst>.partial` records each completed file (size, mtime, hash)
after the file is fsynced. If a run stops, the next run of the same job resumes
from the journal and finishes. A half-written file is simply overwritten.

How much the resume trusts the journal depends on how the earlier run ended:

- Clean stop (deadline reached or SIGTERM): the run flushes the journal and
  writes a `clean-stop` record. Files journaled before it are kept without being
  hashed again, provided the copy in `.partial` still has the journaled size and
  mtime. They were fsynced by this tool before they were journaled. This is what
  lets a large first backup finish over several `--deadline` windows: each window
  spends its time on new data instead of re-hashing the earlier windows' data.
- Unclean stop (SIGKILL, crash, power loss): no such record exists, so every
  journaled file is hashed again and kept only if it still matches. A file that
  does not match is copied again.

A journaled file whose source mtime is at or after the first run's start (minus
2 seconds) is always copied again, for the same reason as above.

Before it resumes and again before it writes the manifests, the run removes
everything in its own `.partial` that is not in the current plan: files deleted
from the source since the interruption, half-written or temporary objects, and
objects whose type changed (a file that became a directory or a symlink, or the
reverse), so they can be recreated. The removal goes through the `safedelete`
package and is confined to the proven `.partial` directory; the journal is never
removed that way.

## Deadlines

`--deadline` (absolute time, or a duration like `2h30m` from run start) makes
the run stop cleanly between files or buffer chunks. The `.partial` directory is
kept, the journal gets a `clean-stop` record, the state is `partial`, and the
exit code is 3. The next run resumes it and does not re-hash the files of that
window.

## Symlinks, special files, hardlinks, sparse files

- Symlinks are recreated verbatim and never followed.
- Sockets, FIFOs and device files are skipped and logged.
- Hardlinked files in the source become independent files in the backup.
- Sparse files are copied as dense data.
- Unreadable source files fail the run (exit 1) and keep the `.partial`.

## Known limits

- Memory grows with the number of files: the walk result, the previous
  manifest and the resume journal are all held in memory, roughly 750 bytes per
  file in total (about 7 GB at 10 million files).
- Retention runs only after a successful backup. If the destination fills up
  mid-run, the partial keeps its data and every later run is refused (exit 4,
  not enough space) until space is freed, for example with `rm` on an old backup.
- Sparse files are stored densely and hardlinked source files become
  independent copies, so a backup can be larger than the source's disk usage.

## Copying without shortcuts

Data moves through an explicit buffered read/write loop. There is no
`copy_file_range`, reflink, clone or hardlink, so on any filesystem the backup
owns its blocks. Source files are opened read-only (with `O_NOATIME` when
permitted).
