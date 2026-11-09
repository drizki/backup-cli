# Contributing

Thanks for helping. backup-cli is deliberately small; the goal is a dependable
tool that stays easy to read.

## Ground rules

- Standard library only. `go.mod` has no `require` lines. Tests use only the
  `testing` package. Pull requests that add a Go module will not be merged.
- Never weaken a safety property: read-only source, independent copies, atomic
  commit, and the single guarded deletion path (see
  [docs/safety-model.md](docs/safety-model.md)).
- Every feature ships with tests for success, failure and edge cases.
- No TODO/FIXME/XXX/HACK comments, no placeholder code, no skipped tests (a
  skip needs an environment reason and an entry in the check script's allowlist).

## Code quality rules

- Simple over clever; no speculative abstractions or single-implementation
  interfaces except for test fakes.
- Guard clauses and early returns; no `else` after `return`; nesting depth 3 at most.
- Files: non-test `.go` at most 400 lines, tests at most 700. Split by responsibility.
- Functions: at most 60 lines and 40 statements; cyclomatic complexity 15,
  cognitive complexity 20.
- Stream data, reuse buffers, avoid per-file goroutines and repeated stat calls in hot loops.

## Workflow

```sh
make check      # fmt vet size complete covergate lint vuln shellcheck
make test       # quick unit run
make race       # tests with the race detector
make build      # builds ./backup-cli
```

You need Go (version in `go.mod`), `golangci-lint`, `govulncheck` and `shellcheck`.
CI runs the same targets.

Coverage gates: at least 85% overall and at least 90% for the engine, copy,
manifest, journal, safe-delete, retention and path-guard packages.

## Commits and pull requests

Use [Conventional Commits](https://www.conventionalcommits.org/): `feat:`,
`fix:`, `docs:`, `test:`, `refactor:`, `ci:`, `build:`, `chore:`. Mark breaking
changes with `!` or a `BREAKING CHANGE:` footer. release-please derives version
numbers and the changelog from these, so the type matters.

Keep commits small and coherent. Update docs when you change a command, flag,
exit code or file format. In examples and tests use generic paths such as
`/srv/data`, `/mnt/share` and `/mnt/backup`; do not commit real hostnames, paths or data.

## Reporting bugs and security issues

Use the issue templates for bugs. For vulnerabilities see [SECURITY.md](SECURITY.md).
