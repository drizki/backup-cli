# Releasing

Every release is a git tag `vMAJOR.MINOR.PATCH` (strict SemVer, for example
`v1.0.0`). The tag is the single source of truth: the binary's `version` output,
the archive names and the GitHub Release name all equal the tag. Builds made
without a tag report `dev`.

## Flow

1. Commit to `main` using Conventional Commits (`feat:`, `fix:`, `feat!:` or a
   `BREAKING CHANGE:` footer).
2. [release-please](https://github.com/googleapis/release-please) keeps a
   release pull request open. It computes the next version from the commits and
   updates `CHANGELOG.md`.
3. Merging the release PR creates the tag and the GitHub Release.
4. The release build runs for that tag (below) and uploads the assets.

Bump rules: `fix:` gives a patch, `feat:` a minor, a breaking change a major.

## Why the build runs inside the release-please workflow

Tags and releases created with the default `GITHUB_TOKEN` do not start other
workflows. So `release-please.yml` calls `release.yml` directly as a reusable
workflow when a release was created, passing the tag. `release.yml` also has a
`push: tags: v*.*.*` trigger, which covers tags pushed by hand or by a personal
access token. Do not switch release-please to a token that triggers workflows
without removing the direct call, or the build would run twice.

Repository settings needed: Actions workflow permissions "Read and write", and
"Allow GitHub Actions to create and approve pull requests".

## What the release workflow does

1. Checks out the tag with full history.
2. Verifies the tag is strict `vMAJOR.MINOR.PATCH` and that its commit is
   reachable from `main`. Otherwise it fails.
3. Runs `scripts/release-build.sh <tag> dist`, which builds linux amd64 and
   arm64 with `CGO_ENABLED=0 go build -trimpath` and linker flags that set
   `internal/version.Version` (the tag), `Commit` and `Date`, then packages
   `backup-cli_<tag>_linux_<arch>.tar.gz` (binary, `README.md`, `LICENSE`) and
   writes `checksums.txt` with SHA-256.
4. Creates build provenance attestations for the archives and `checksums.txt`.
5. Uploads everything with `gh release upload`.

You can run step 3 locally: `scripts/release-build.sh v1.2.3 /tmp/dist`.

## Verifying a download

Checksums:

```sh
sha256sum -c --ignore-missing checksums.txt
```

Provenance (needs the GitHub CLI):

```sh
gh attestation verify backup-cli_v1.0.0_linux_amd64.tar.gz --repo drizki/backup-cli
```

`install.sh` always verifies the checksum before installing.

## Manual tag

If you ever need to tag by hand: create an annotated tag `vX.Y.Z` on a commit
that is on `main` and push it. The release workflow builds and publishes it
(creating the GitHub Release if it does not exist). Keep the release-please
manifest in step by editing `.release-please-manifest.json` in a commit.
