---
name: release
description: Prepare a weightkeep release by reviewing and editing the release-please PR's changelog. Only when the user explicitly asks to release.
disable-model-invocation: true
allowed-tools: Bash(gh pr list:*) Bash(gh pr view:*) Bash(gh pr diff:*) Bash(make check) Bash(git --no-pager log:*)
---

# Release

Releases are cut by merging the release-please PR (see `docs/contributing/commits.md#releasing`).
This skill prepares that PR; the user merges it.

1. `make check` on `main`. Stop if anything fails.
2. Find the PR: `gh pr list --label "autorelease: pending"`.
3. Read its `CHANGELOG.md` diff and `git --no-pager log <last tag>..main --oneline`.
4. Rewrite the new changelog section for users, following the docs-voice skill:
   - one short paragraph at the top: what this release is about,
   - group entries under Added / Changed / Fixed / Security, merge duplicates, drop internal noise
     (refactors, CI) unless users would notice,
   - call out anything that needs action on upgrade, first.
5. Show the user the proposed text. Push the edit to the release PR branch only after they approve.
6. After they merge: check the GitHub release page, the Homebrew tap commit, and that
   `gh attestation verify` works on one downloaded archive.

Never create tags by hand and never run GoReleaser locally with `--clean` against the real repo.
