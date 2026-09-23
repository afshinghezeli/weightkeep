# Writing style

For the README, docs, CLI help text, error messages and release notes.

## Principles

1. Say what it does, then what it doesn't. Limits belong in the main text, not a footnote.
2. Specific beats general. "Resumes a 40 GB download from two peers after the Hub returned 404" says
   more than any adjective.
3. Numbers come with how they were measured: machine, link speed, date, command.
4. Prose for explaining, lists for options and steps. A "why" section is paragraphs.
5. Sentence case for headings. No emoji in docs, headings or CLI output.
6. Bold at most once or twice per screen. Most pages need none.
7. Vary sentence length. A one-line paragraph is fine.
8. Read it aloud. Cut what you wouldn't say to a colleague.

## Words to avoid

These are filler at best, and at worst they read as copy that nobody stood behind. The docs hook in
`.claude/hooks/check-prose.sh` flags them in Markdown files.

seamless, robust, leverage, cutting-edge, state-of-the-art, blazing, blazingly, supercharge,
effortless, elevate, empower, unlock, streamline, harness, delve, tapestry, testament, pivotal,
game-changer, revolutionize, revolutionary, next-generation, holistic, "in today's", "look no further",
"it's worth noting", "at its core", "dive in", "deep dive", "whether you're".

Also avoid starting sentences with "Additionally", "Moreover" or "Furthermore", and cut "simply",
"just" and "easily" when they only reassure. "Robust" and "comprehensive" are allowed in code
comments about tests, where they mean something measurable; not in prose.

## Patterns to avoid

- `## Features` followed by a wall of bullets that each start with a **Bold label:**.
- Lists of three adjectives ("fast, reliable, and secure").
- "It's not just X, it's Y."
- Em dash chains. Use a comma, a colon, parentheses or a new sentence.
- A closing summary or "Conclusion" section.
- Claims about users you don't have ("developers love", "trusted by").
- Badges that carry no information (made-with, PRs-welcome, visitor counters).

## CLI output and errors

- Lower case, no trailing period, as Go errors are: `open blob 3f2a…: permission denied`.
- An error says what failed and, when we know, what to do: `repo is gated; weightkeep never seeds
  gated repos (see docs/licences.md)`.
- Progress output goes to stderr; data (`ls --json`, `manifest`) goes to stdout.
- Don't print anything on success unless the command's purpose is to print.

## Release notes

Written for someone deciding whether to upgrade. What changed for them, what they need to do, who
helped. Not a commit log.
