---
status: accepted
date: 2026-09-24
---

# Conventional Commits, release-please, GoReleaser

## Context and problem statement

We want a changelog people actually read, version numbers that mean something, and releases that do
not depend on one person's laptop.

## Considered options

1. Hand-written changelog, manual tags.
2. Conventional Commits, git-cliff, manual tag, GoReleaser.
3. Conventional Commits, release-please release PRs, GoReleaser on tag.

## Decision outcome

Chosen option 3.

- Commit messages and PR titles follow Conventional Commits 1.0. PRs are squash-merged and the PR
  title becomes the commit, so only the title is linted in CI.
- release-please keeps an open release PR that bumps the version and updates `CHANGELOG.md`. We edit
  the changelog in that PR by hand before merging. Merging tags `vX.Y.Z`.
- GoReleaser builds on the tag: archives for linux/darwin/windows on amd64/arm64, checksums, SBOM,
  build provenance attestation, Homebrew tap, Scoop, deb/rpm.
- Versioning is SemVer. While in 0.x, a breaking change bumps the minor and everything else bumps
  the patch (`bump-minor-pre-major`, `bump-patch-for-minor-pre-major`).
- Go module path stays `github.com/afshinghezeli/weightkeep` through 1.x; a 2.0 would need `/v2`.

Rules for writing commits are in [`docs/contributing/commits.md`](../contributing/commits.md).

### Consequences

- Good: changelog and version bump come from history we already write.
- Bad: contributors must learn the title format. The PR title check tells them what is wrong.
