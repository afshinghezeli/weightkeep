---
name: adr
description: Create a new architecture decision record in docs/adr. Use when a change touches the store layout, database schema, manifest format, signed data, proxy wire behaviour, licence policy, or a hard-to-swap dependency, or when the user asks for an ADR.
argument-hint: "\"short decision title\""
---

# New ADR

Existing records: !`ls docs/adr/`

1. Next number: one more than the highest `NNNN-*.md` above, zero-padded to 4.
2. File name: `docs/adr/NNNN-kebab-case-title.md`, title from `$ARGUMENTS`.
3. Copy the structure of `docs/adr/template.md`. Front matter `status: proposed` and today's date.
4. Fill it from what is actually known in this session. Context in 2-4 sentences. List real options
   that were considered, including the one we rejected and why. Consequences must include at least one
   honest "Bad".
5. Under one page. Follow the docs-voice skill.
6. Add a row to the table in `docs/adr/README.md`.
7. If this supersedes an earlier ADR, change only the old one's front matter to
   `status: superseded by NNNN` and leave its body alone.

Commit it on its own: `docs(adr): record <decision>`. An ADR that accompanies code can go in the same
PR, but as a separate commit before the code.
