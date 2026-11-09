# backup-cli

[![CI](https://github.com/drizki/backup-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/drizki/backup-cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/drizki/backup-cli)](https://github.com/drizki/backup-cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/drizki/backup-cli)](https://goreportcard.com/report/github.com/drizki/backup-cli)

**Full, independent backups at incremental speed. Restore with `cp`.**

backup-cli makes a complete, self-contained copy of a directory on every run,
yet only reads what changed from the source. Every backup is a plain directory
of plain files. No repository format, no chain of increments, no special tool
needed to get your data back.

## The problem

Most backup tools make you choose:

- Simple full copies are easy to trust and easy to restore, but slow and heavy
  to repeat on large trees.
- Incremental or deduplicating tools are fast, but your data lives in a
  repository format or a chain of deltas, and you need the tool (and an intact
  chain) to read it back.

## How backup-cli does it

Each run produces a full copy. Files that did not change since the previous
backup are copied from that previous backup on the destination disk, not from
the source. Only new or modified files are read from the source. You get the
speed of an incremental backup and the result of a full one.

Every backup is independent: no hardlinks, no reflinks, no shared blocks.
Delete any backup and every other one is untouched. Restore with `cp -a`.

## Features

- Full independent copy per run; restore is a plain `cp -a` or `rsync -a`.
- Unchanged files are copied from the previous local backup and re-hashed on
  the way, so silent corruption in an old backup is detected and repaired from
  the source.
- Single-pass SHA-256 for every file; `MANIFEST.sha256` works with `sha256sum -c`.
- Atomic commit: a backup is built in `<name>.partial` and renamed only when
  complete, with a marker file written last.
- Resume after interruption; optional `--deadline` that stops cleanly.
- Retention: keep the newest N backups, pruned only after a verified success.
- Scheduling through systemd timers (daily, weekly, biweekly, monthly, or any
  `OnCalendar` expression). Without systemd it prints the cron line instead.
- Free-space pre-check, post-commit sample verification, hooks on success and
  failure, `doctor` for diagnosis, `--json` output.
- Static Go binary, standard library only, no runtime dependencies.

## Safety guarantees

- The source is only ever opened read-only. It is never modified.
- Symlinks are copied as symlinks and never followed. Special files are skipped
  and logged.
- Source and destination may not overlap (checked in both directions, through
  symlinks).
- One run per job at a time (file lock).
- All deletion goes through a single guarded code path: it requires a valid
  marker, a matching job, a location under the job's destination parent, and
  refuses the source, the state directory, mount points, and `/`.
- Nothing is pruned after a failed, partial, refused or dry run.
- `--dry-run` writes nothing anywhere.

Details: [docs/safety-model.md](docs/safety-model.md).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/drizki/backup-cli/main/install.sh | sh
```

Linux amd64 and arm64. The installer downloads the release archive and
`checksums.txt`, verifies SHA-256, and only then installs to `/usr/local/bin`.
Use `--version vX.Y.Z`, `--prefix DIR`, or `--uninstall`. To inspect before
running, download `install.sh` first. Release assets also carry build
provenance attestations; see [docs/releasing.md](docs/releasing.md).

## 60-second quickstart

```sh
# Register a job: source, destination template, schedule, retention.
sudo backup-cli add /srv/data /mnt/backup/data-{timestamp} \
  --schedule daily,02:30 --keep 7 --now

backup-cli list jobs
backup-cli list runs
backup-cli tail <run-id>        # follow a run; id from `list runs`
backup-cli list backups
backup-cli verify <backup>      # re-hash against the manifest

# Restore: it is just files.
cp -a /mnt/backup/data-20260101T023000Z/. /srv/restore/
```

Try a run without touching anything: `backup-cli run <job> --dry-run`.

## How a run works

```
 source (read-only)            previous backup (local)
        |                              |
        |  walk metadata               |  load MANIFEST.tsv
        +------------+-----------------+
                     v
        compare path + size + mtime
          |                      |
     changed / new           unchanged
          |                      |
   read from source       copy from previous backup
   hash while copying     re-hash, compare with manifest
          |                      |
          +----------+-----------+
                     v
          <dst>.partial/   (journal, fsync per file)
                     v
       MANIFEST.sha256 + MANIFEST.tsv + .backup-cli.json
                     v
          atomic rename -> <dst>/  (committed)
                     v
       sample verify -> prune old backups (guarded)
```

More in [docs/how-it-works.md](docs/how-it-works.md).

## Commands

| Command | Purpose |
|---|---|
| `add` | Register a job and install its schedule |
| `run <job>` | Run a job now (`--dry-run`, `--from-source`, `--force`) |
| `list jobs\|runs\|backups` | Show jobs, runs or committed backups (`--json`) |
| `tail <run-id>` | Follow a run's log |
| `verify <backup>` | Check a backup against its manifest |
| `rm <backup>` | Delete one backup, with guards and confirmation |
| `job rm <job>` | Remove a job and its timer; backups are kept |
| `doctor` | Diagnose the environment (`--json`) |
| `version`, `help` | Version and usage |

Every command and flag, plus the exit code table: [docs/commands.md](docs/commands.md).

## FAQ

**Why not `rsync --link-dest`?**
It is excellent and very close in spirit. The difference: link-dest backups
share inodes through hardlinks, so they are not independent. Corruption of one
shared file damages every backup that links to it, and deleting or copying a
tree elsewhere needs care. backup-cli stores real, separate files and verifies
every file by hash. If you are happy with hardlink semantics, link-dest is
simpler and needs nothing installed.

**Why not restic or borg?**
They deduplicate, compress and encrypt, and they are the right choice for
offsite, encrypted, space-efficient backups. The price is a repository format:
you need the tool and a healthy repository to restore. backup-cli does none of
that on purpose. It trades disk space for the ability to restore with `cp`.
There is no encryption or compression; use an encrypted filesystem or
transport if you need them.

**Why not filesystem snapshots (ZFS, btrfs, LVM)?**
Snapshots are instant and cheap, and you should use them. But they live on the
same pool or volume as the data, so they do not protect against losing the
pool. backup-cli writes independent files to a different disk or share. They
combine well: back up from a snapshot mount.

**Does it use hardlinks, reflinks or `copy_file_range`?**
No. Data is copied through an explicit read/write loop so every backup owns
its blocks, even on filesystems that would otherwise share them.

**Does it need root?**
Not to run. Ownership (uid/gid) is preserved only when run as root. Installing
systemd units under `/etc/systemd/system` needs root.

**Is it incremental?**
It reads incrementally and stores fully. Each backup is complete.

## Documentation

[How it works](docs/how-it-works.md) · [Safety model](docs/safety-model.md) ·
[Commands](docs/commands.md) · [Scheduling](docs/scheduling.md) ·
[Restore](docs/restore.md) · [Doctor](docs/doctor.md) ·
[State layout](docs/state-layout.md) · [Releasing](docs/releasing.md)

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) and [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
