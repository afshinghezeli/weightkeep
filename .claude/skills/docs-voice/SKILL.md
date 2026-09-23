---
name: docs-voice
description: How weightkeep's README, docs, ADRs, CLI help, error messages and release notes are written. Use whenever writing or editing Markdown, CLI help strings, user-facing error text, or release notes in this repo.
paths:
  - "**/*.md"
  - "internal/cli/**"
---

# Docs voice

The rules are in `docs/contributing/style.md`. Read it before writing. The PostToolUse hook
`.claude/hooks/check-prose.sh` flags the banned words after every Markdown edit; fix what it reports
by saying something specific, not by swapping in a synonym.

## Checklist before finishing a doc edit

- Does the first sentence say what the thing does, in plain words?
- Is every claim either measured (with how) or removed?
- Are limits stated where the feature is described?
- Headings in sentence case, no emoji, no "Features"/"Conclusion" sections.
- Bullets only for options, steps and reference tables. Explanations are paragraphs.
- At most one em dash on the page. Prefer none.
- Examples use tier A models only (see the licence-policy skill).
- Commands in examples were actually run, and output shown is real output.

## What only the maintainer writes

The README's opening paragraphs and "why" section, the Show HN post, Reddit posts, and replies in
issues and discussions are written by the maintainer in their own words. Hacker News forbids
AI-generated or AI-edited comments (guidelines, March 2026). For those, draft an outline of facts and
leave `<!-- maintainer: write this -->` markers instead of prose.

## CLI text

- Help: one line for `Short`, a short paragraph plus an example for `Long`.
- Errors: lower case, no trailing period, what failed and what to do next.
- Nothing on stdout except the command's data. Progress and hints go to stderr.
