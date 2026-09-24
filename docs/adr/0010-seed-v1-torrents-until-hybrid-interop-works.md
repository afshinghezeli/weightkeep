---
status: accepted
date: 2026-09-24
---

# Seed v1 torrents with BEP 47 padding until hybrid interop works

## Context and problem statement

ADR 0005 chose hybrid v1+v2 torrents. The builder produces them byte-identical to libtorrent 2.1.1,
but when libtorrent downloads a hybrid torrent from an anacrolix/torrent seeder, anacrolix drops the
connection with "chunk overflows piece". anacrolix v1.61.0 sizes the last piece of each file by the
file's length for any torrent with v2 data (`metainfo.Piece.Length`), while libtorrent requests that
piece padded to the full piece length, as the v1 half of a hybrid describes it. Neither side can serve
the other a file's last piece. anacrolix's unreleased `master` (2026-09-11) didn't fix this and hung
our own seeder-to-leecher test.

## Decision drivers

- Other clients (libtorrent, qBittorrent, Transmission) must be able to download what we seed.
- A seeder that keeps only some files of a revision should still serve those files.
- Don't fork the BitTorrent library for this.

## Considered options

1. Keep hybrid torrents and patch anacrolix's piece length and storage for hybrids.
2. v1 torrents without padding, the most widely supported form.
3. v1 torrents with BEP 47 padding: files sorted and each padded to a piece boundary, exactly what
   libtorrent writes with `v1_only | canonical_files`.

## Decision outcome

Chosen option 3, until anacrolix handles hybrid torrents the way libtorrent does.

- `torrent.Build` keeps both modes; seeding uses `Options{V1Only: true}`. Both are tested byte for byte
  against libtorrent.
- Files stay piece-aligned, so every piece belongs to one file and a partial seeder can serve whole
  files.
- The seeder maps each pad file onto a sparse zeros file of the same length (anacrolix checks file
  sizes, pads included).
- The manifest's plain SHA-256 remains the check that matters; v2 merkle roots can still be computed
  by the builder when needed.

### Consequences

- Good: libtorrent downloads a revision from a store-backed anacrolix seeder (tested).
- Good: no fork of anacrolix.
- Bad: no per-file v2 roots in the swarm, so identical files in different torrents are separate
  swarms, and there's no `btmh` magnet.
- Bad: moving to hybrid later changes every info hash, which starts new swarms.

## More information

Revisit when anacrolix releases a version whose `Piece.Length` treats hybrid torrents as padded v1;
`TestLibtorrentLeechesFromStore` with `V1Only: false` is the check. ADR 0005's web seed design is
unaffected.
