# Architecture decision records

Decisions that are expensive to reverse: on-disk formats, wire compatibility, trust model, licence
policy, language and dependencies. Format is [MADR 4](https://adr.github.io/madr/), trimmed.

Write a new one with `/adr "title"` in Claude Code, or copy [`template.md`](template.md) to the next
free number. An accepted ADR is not edited except to change its status; supersede it with a new one.

| # | Decision | Status |
| --- | --- | --- |
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | accepted |
| [0002](0002-go.md) | Write it in Go | accepted |
| [0003](0003-content-addressed-store.md) | Content-addressed store keyed by SHA-256, metadata in SQLite | accepted |
| [0004](0004-hub-compatible-proxy.md) | Imitate the Hub API, strip Xet | accepted |
| [0005](0005-bittorrent-v2-with-hub-webseeds.md) | Hybrid v1/v2 torrents with Hub web seeds | accepted, amended by 0010 |
| [0006](0006-registry-trust-model.md) | TUF registry of revision manifests | accepted |
| [0007](0007-licence-tiers.md) | Licence tiers decide what may be shared | accepted, amended by 0009 |
| [0008](0008-commits-and-releases.md) | Conventional Commits, release-please, GoReleaser | accepted |
| [0009](0009-licence-texts-for-repos-without-a-licence-file.md) | Supply licence texts for permissively licensed repos without a LICENSE file | accepted |
| [0010](0010-seed-v1-torrents-until-hybrid-interop-works.md) | Seed v1 torrents with BEP 47 padding until hybrid interop works | accepted |
