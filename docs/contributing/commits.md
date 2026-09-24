# Commits, branches and versions

This is the reference. [ADR 0008](../adr/0008-commits-and-releases.md) has the reasoning.

## Commit messages

[Conventional Commits 1.0](https://www.conventionalcommits.org/en/v1.0.0/):

```
<type>(<scope>): <summary>

<body: why, not what. Wrapped at 72.>

<footers>
```

### Types

| Type | Use for | Version effect (0.x) |
| --- | --- | --- |
| `feat` | New behaviour a user can notice | patch |
| `fix` | A bug fix a user can notice | patch |
| `perf` | Faster or smaller, same behaviour | patch |
| `refactor` | Code change with no behaviour change | none |
| `test` | Tests only | none |
| `docs` | Documentation only | none |
| `build` | go.mod, Makefile, GoReleaser | none |
| `ci` | GitHub Actions | none |
| `chore` | Anything else that ships nothing (tooling, repo settings) | none |
| `revert` | Reverting an earlier commit | depends |

A breaking change adds `!` after the type/scope (`feat(store)!: ...`) and a `BREAKING CHANGE:` footer
explaining what users must do. In 0.x that bumps the minor version.

### Scopes

Use the package or area touched. One scope; if a change spans several, pick the one users would care
about, or leave the scope out.

`cli`, `config`, `hub`, `ids`, `keep`, `store`, `manifest`, `fetch`, `hfcache`, `proxy`, `policy`,
`torrent`, `registry`, `seed`, `testutil`, `deps`, `release`. (`docs` and `ci` are types, not scopes.)

### Summary line

- Imperative, lower case after the colon, no trailing period: `fix(proxy): send X-Repo-Commit on 404`.
- 50 characters is the target, 72 the hard limit.
- Say what changed for the user or the code, not which file you edited.

### Body

Optional for small changes. When present, explain why the change is needed and anything surprising
about how. Don't restate the diff. Reference issues in footers: `Fixes #12`, `Refs #40`.

### Examples

```
feat(proxy): answer /api/models/{repo}/refs

llama.cpp builds from March 2026 resolve the commit through /refs
before listing the tree. Without it `-hf` fails with "failed to
resolve commit".
```

```
fix(store): keep blob mode 0444 after rename on Windows
```

```
feat(store)!: key refs by repo type as well as id

BREAKING CHANGE: stores created by 0.1.x need `weightkeep migrate`
before 0.2 can open them.
```

## Commit hygiene

- One logical change per commit. A feature and the refactor it needed are two commits.
- Every commit on `main` builds and passes `make check`.
- Never commit generated binaries, `.notes/`, local state, tokens or real model files.
- Stage files by name. Don't use `git add -A` or `git add .`.
- No AI attribution trailers (`Co-Authored-By: <assistant>`, "Generated with ..."). Disclosure of AI
  tooling goes in the PR description, per [CONTRIBUTING](../../CONTRIBUTING.md#ai-assisted-contributions).
- Sign commits if you can (`git config commit.gpgsign true` with an SSH or GPG key).

## Branches and pull requests

- `main` is always releasable. Work happens on short-lived branches: `feat/proxy-refs`,
  `fix/store-windows-mode`, `docs/readme-install`.
- PRs are squash-merged. The PR title becomes the commit subject on `main`, so it must follow the
  format above; CI checks it.
- Keep PRs small enough to review in one sitting. Split otherwise.

## Versions

SemVer 2.0 with the Cargo/npm convention for 0.x:

- `0.y.z`: a breaking change bumps `y`, everything else bumps `z`.
- 1.0.0 once the CLI flags, config file, store layout and manifest format are stable.
- Pre-releases: `0.2.0-rc.1`.
- Tags are `vX.Y.Z` and are created only by merging the release-please PR.

What counts as breaking:

- Removing or renaming a command or flag, or changing its default behaviour.
- A store or database change that old versions cannot read, or that needs a migration.
- Changing the manifest format or anything that is signed.
- Dropping a proxy endpoint or changing a response a documented client depends on.

## Releasing

1. release-please keeps a PR named `chore(release): X.Y.Z` up to date.
2. Before merging it, edit `CHANGELOG.md` in that PR so it reads well for users. Group related
   entries, drop noise, add a one-paragraph summary at the top of the release. Do this last: every
   push to `main` regenerates the PR and overwrites manual edits.
3. Merge with `gh pr merge <n> --squash --admin`. GitHub doesn't run workflows for pull requests
   opened with the default Actions token, so the release PR never gets its required checks. It only
   touches `CHANGELOG.md` and `.release-please-manifest.json`, and everything it releases already
   passed CI on `main`. (A GitHub App token for release-please would remove this step.)
4. The merge creates the tag and the GitHub release; the same workflow run builds the binaries with
   GoReleaser and attests them.
5. Check the GitHub release page and edit the notes if needed.
