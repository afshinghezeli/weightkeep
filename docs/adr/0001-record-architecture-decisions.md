---
status: accepted
date: 2026-09-24
---

# Record architecture decisions

## Context and problem statement

weightkeep writes files to users' disks, speaks another service's wire protocol, and will publish
signed metadata that other people rely on. Getting any of those wrong is expensive to undo, and
contributors (including future us) need to know why things are the way they are.

## Decision outcome

Keep short ADRs in `docs/adr/` using a trimmed MADR 4 template. One decision per file, numbered,
never rewritten after acceptance; a later ADR supersedes an earlier one.

Write an ADR before changing:

- the on-disk store layout or the SQLite schema in a way that needs migration,
- the manifest format or anything signed,
- which Hub endpoints the proxy implements or how it answers them,
- the licence policy,
- a direct dependency that is hard to swap (BitTorrent, SQLite, TUF).

### Consequences

- Good: reviewers can point at a record instead of re-arguing.
- Bad: small overhead per decision. Keep them under a page.
