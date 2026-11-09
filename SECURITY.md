# Security policy

## Reporting a vulnerability

Please report security issues privately using GitHub private vulnerability
reporting: <https://github.com/drizki/backup-cli/security/advisories/new>.
Do not open a public issue for a vulnerability.

Include the version (`backup-cli version`), what you observed, and a minimal
reproduction if you can. You will get an acknowledgement, and a fix and a
coordinated disclosure will follow for confirmed issues.

## Scope

Of particular interest: anything that could make backup-cli modify or delete
data outside a job's own backups, follow a symlink out of its intended tree,
write to the source, bypass a deletion guard, or commit a backup that is
incomplete. Path traversal through manifests or file names is in scope.

## Supported versions

Only the latest release receives fixes.

## Verifying releases

Release archives come with `checksums.txt` and GitHub build provenance
attestations. See [docs/releasing.md](docs/releasing.md).
