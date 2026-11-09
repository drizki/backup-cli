# Restore

A backup is a normal directory tree. You do not need backup-cli to restore.

## Restore everything

```sh
cp -a /mnt/backup/data-20260101T023000Z/. /srv/restore/
# or
rsync -a /mnt/backup/data-20260101T023000Z/ /srv/restore/
```

Run as root to restore ownership (`-a` preserves uid/gid only for root).

## Restore one file or directory

```sh
cp -a /mnt/backup/data-20260101T023000Z/projects/report.txt /srv/restore/
```

## Extra files in a backup

Each backup root contains three files that are not part of your data:

- `MANIFEST.sha256`: hashes in `sha256sum` format.
- `MANIFEST.tsv`: path, type, size, mtime, mode, ownership, hash, link target.
- `.backup-cli.json`: the marker (job, run id, counts, hash of the TSV manifest).

Exclude them when restoring if you do not want them:

```sh
rsync -a --exclude=/MANIFEST.sha256 --exclude=/MANIFEST.tsv \
  --exclude=/.backup-cli.json /mnt/backup/data-20260101T023000Z/ /srv/restore/
```

## Verify before or after restoring

With standard tools:

```sh
cd /mnt/backup/data-20260101T023000Z && sha256sum -c MANIFEST.sha256
```

Or with backup-cli, which also checks the TSV manifest and flags missing and extra files:

```sh
backup-cli verify /mnt/backup/data-20260101T023000Z
backup-cli verify /mnt/backup/data-20260101T023000Z --sample 10
```

## What is and is not preserved

Preserved: contents, modes, mtimes (nanoseconds), symlinks (as links), empty
directories, ownership when the backup ran as root.

Not preserved: hardlink relationships (each link becomes its own file), sparse
holes (files are dense), sockets/FIFOs/devices (skipped), extended attributes and ACLs.
