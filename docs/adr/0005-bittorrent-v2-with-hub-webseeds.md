---
status: proposed
date: 2026-09-24
---

# Hybrid v1/v2 torrents with Hub web seeds

## Context and problem statement

When the Hub cannot serve a file, weightkeep needs another source that nobody has to run as a
service. The most common objection to torrent-based model archives is that swarms die once the
initial enthusiasm fades.

## Decision drivers

- A torrent that can always fall back to an HTTP source never has zero seeds while that source exists.
- Identical files appear in many revisions; they should be one swarm, not many.
- Third-party clients (qBittorrent, Transmission 4) should be able to use our torrents without weightkeep.

## Considered options

1. v1 torrents, one per file.
2. v2-only torrents, one per revision.
3. Hybrid v1+v2 torrents, one per revision, with the Hub as a BEP 19 web seed.
4. IPFS as the primary P2P layer.

## Decision outcome

Chosen option 3.

- `info.name` is the 40-hex commit SHA and `url-list` is `https://huggingface.co/<org>/<repo>/resolve/`,
  so BEP 19 builds `.../resolve/<commit>/<path>` for each file. Commit-pinned URLs never change content.
- Piece size 4-16 MiB, to keep piece layers small and to stay well inside the Hub's resolver rate
  limit (3,000 requests per 5 minutes per anonymous IP).
- We write our own hybrid builder on top of `anacrolix/torrent/merkle` and `metainfo`, because
  anacrolix only builds v1. It computes SHA-1 pieces, v2 merkle roots and plain SHA-256 in one pass.
- The v2 per-file root is not the plain SHA-256 of the file (they differ above 16 KiB). The manifest
  stores both.
- The web seed transport re-resolves every request (signed CDN URLs expire after about an hour) and
  only attaches an `Authorization` header when talking to huggingface.co.

IPFS is supported only as an HTTP mirror type (gateways or a user's own Kubo), not embedded.

### Consequences

- Good: swarms survive as long as the Hub or any peer has the file.
- Good: a seeder holding a blob serves it in every torrent that contains it.
- Bad: we own a torrent builder and must test it against libtorrent's output.
- Bad: v1-only clients join a separate swarm from v2 clients for the same content. Hybrids limit the damage.

## More information

Spikes before acceptance: (1) anacrolix web seeds against the Hub for more than an hour; (2) our
hybrid torrents load in qBittorrent and match libtorrent's infohashes for the same input.
