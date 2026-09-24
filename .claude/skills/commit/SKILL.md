---
name: commit
description: Commit the current work in this repo following its Conventional Commits rules. Use when the user asks to commit, or at the end of each roadmap task once checks pass.
allowed-tools: Bash(git status:*) Bash(git --no-pager diff:*) Bash(git --no-pager log:*) Bash(git add:*) Bash(git commit:*) Bash(make check) Bash(make lint) Bash(make test)
---

# Commit

Rules: `docs/contributing/commits.md`. Summary of the procedure:

## 1. Look before staging

```sh
git status --short
git --no-pager diff --stat
```

Decide how many commits this is. One logical change per commit: a refactor and the feature that needed
it are two commits; a feature and its tests are one.

## 2. Check

If any `.go` file changed: `make check` must pass. Don't commit red.
If only docs changed: the prose hook has already run; fix anything it reported.

## 3. Stage by name

`git add path/one path/two`. Never `git add -A`, `git add .` or `git commit -a`.
Never stage `.notes/`, `bin/`, `dist/`, `.weightkeep/`, `CLAUDE.local.md`, tokens, or model files.
When the user asked only for a commit message, commit what they staged and add nothing.

## 4. Read the staged diff

```sh
git --no-pager diff --cached
```

Write the message from the diff and from what was done in this session (the why), not from file names.

## 5. Message

```
<type>(<scope>): <imperative summary, lower case, no period, ≤50 chars ideally, ≤72 max>

<optional body wrapped at 72: why this change, anything non-obvious about how>

<optional footers: Fixes #N, Refs #N, BREAKING CHANGE: ...>
```

- Types: feat, fix, perf, refactor, test, docs, build, ci, chore, revert.
- Scopes: cli, config, hub, ids, keep, store, manifest, fetch, hfcache, proxy, policy, torrent,
  registry, seed, testutil, deps, release. Omit when the change spans areas. A new package means a new
  scope: add it to `.github/workflows/pr-title.yml` and `docs/contributing/commits.md` first.
- Breaking: `!` after the scope plus a `BREAKING CHANGE:` footer.
- No `Co-Authored-By` trailers, no "Generated with" lines, no emoji.
- The body must not restate the diff ("This commit updates X"). If there is no why worth saying, no body.

## 6. Commit

Use a heredoc so the body keeps its line breaks:

```sh
git commit -F - <<'MSG'
feat(proxy): answer /api/models/{repo}/refs

llama.cpp builds from March 2026 resolve the commit through /refs
before listing the tree.
MSG
```

Show the resulting `git --no-pager log -1 --stat` to the user. Don't push unless asked.
