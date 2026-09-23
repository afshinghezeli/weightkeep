---
status: accepted
date: 2026-09-24
---

# Content-addressed store keyed by SHA-256, metadata in SQLite

## Context and problem statement

The same shard shows up in many places: several revisions of one repo, forks, quant repos that copy
the tokenizer. We need to store each distinct file once, verify it cheaply, and hand it to clients
under whatever name they expect (the Hub's ETag, an Ollama digest, a torrent path).

## Decision drivers

- The Hub already publishes SHA-256 for LFS/Xet files (`lfs.oid`, `X-Linked-Etag`). OMS manifests
  default to SHA-256. Ollama digests are SHA-256. BEP 52 hashes are SHA-256 based.
- Small non-LFS files only have a git blob SHA-1 upstream.
- A crash mid-download must never leave a file under its final name.

## Considered options

1. Mirror the HF cache layout directly (`models--org--name/blobs/<etag>`).
2. Our own CAS keyed by SHA-256, with HF cache views generated on demand.
3. Key by BLAKE3 (faster), map to SHA-256 separately.

## Decision outcome

Chosen option 2.

```
blobs/sha256/<first 2 hex>/<64 hex>     read-only once written
tmp/<random>                            download in progress, same filesystem
weightkeep.db                           SQLite, WAL mode, via modernc.org/sqlite
```

- The blob key is SHA-256 of the content. For every file we also record the git blob SHA-1
  (`sha1("blob <len>\0" + data)`), because that is the Hub ETag for non-LFS files and clients name
  their cache entries after it.
- Downloads stream into `tmp/`, hashing as they go, then `fsync` and `rename`. A blob that exists is
  complete and was verified at write time.
- Revisions are keyed by full 40-hex commit. Branch and tag names live in a `refs` table with the
  time they were resolved; they are hints, not identities.
- HF cache views (`internal/hfcache`) are materialised with reflink, then hardlink, then symlink,
  then copy, in that order. We never write into the Hub client's shared Xet blob directory.

### Consequences

- Good: dedup across repos and revisions for free; one hash to verify against every source.
- Good: the store does not care which client or protocol asked for the bytes.
- Bad: small files get hashed twice (SHA-1 and SHA-256). They are small.
- Bad: two copies of the layout exist when users also run `hf download`. Hardlinks make that free on
  the same volume.

## More information

Schema changes after 0.1.0 require a migration in `internal/store/migrations` and a note in this ADR's
successor.
