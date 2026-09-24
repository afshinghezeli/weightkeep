---
status: accepted
date: 2026-09-24
---

# TUF registry of revision manifests

## Context and problem statement

When a repo disappears from the Hub, the only record of what its files hashed to is whatever someone
saved beforehand. A shared registry of `org/model@commit → file hashes` lets anyone verify a copy from
a stranger's seed box. That registry must survive hostile mirrors, stale mirrors, and a compromised
single maintainer.

## Decision drivers

- Mirrors must be untrusted by design.
- More than one maintainer must agree before trusted metadata changes.
- Rollback and freeze attacks (serving an old denylist, say) must be detectable.
- Manifests should be verifiable with existing model-signing tools, not only ours.

## Considered options

1. minisign signatures on a JSON index.
2. Sigstore keyless signing per manifest.
3. TUF repository (go-tuf v2) with OMS statements as targets.

## Decision outcome

Chosen option 3, a static TUF repository built with go-tuf v2. Details as built in M4.2 and M4.3:

- Authoring happens in a git repo: a pull request adds `records/models/<org>/<name>/<commit>.json`.
  `weightkeep-registry check` re-fetches the Hub tree at that commit and compares every file, and
  refuses gated, private, tier C and denylisted revisions.
- Each target is a weightkeep record: our manifest (the same file list, sizes and SHA-256 that
  `manifest sign` turns into an OMS statement) plus an optional magnet link. We dropped the plan to
  store OMS statements with our extras in TUF custom metadata: an unsigned OMS statement adds nothing
  TUF doesn't already give, and anyone who wants one can produce it from the manifest.
- All roles use ECDSA P-256 keys. Root is signed offline with a threshold. Targets, snapshot and
  timestamp are signed by the registry repo's CI. The timestamp expires after seven days and CI
  re-signs it daily; snapshot 30 days, targets 90, root a year.
- The client is configured with a registry URL and a trusted `1.root.json`, and follows root rotation
  from there. Pinning a root in the binary waits until a public registry with its maintainers' keys
  exists.
- Consistent snapshots are off, so the published directory is plain files any web server can host.

### Consequences

- Good: standard, audited update semantics instead of a homemade scheme. Rollback, freeze and foreign
  keys are each covered by a test.
- Good: our manifest SHA-256 values equal the Hub's own, so a record can be checked against the Hub
  while the Hub still serves the revision.
- Bad: CI holds the targets key, so a compromised CI can sign bad records until the maintainers
  rotate it with the root keys. A threshold of maintainers on targets would close that, at the cost of
  a human signing every publish.
- Bad: every client downloads every record on sync. Fine for thousands of records; beyond that it
  needs per-namespace delegations.

## More information

Revisit if the OpenSSF model-signing project publishes a registry format of its own.
