---
status: proposed
date: 2026-09-24
---

# TUF registry carrying OMS manifests

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

Chosen option 3, proposed until the registry work starts (M4).

- Authoring happens in a git repo: a PR adds `models/<org>/<repo>/<commit>.json`. CI re-fetches the
  Hub metadata, checks licence tier and gating, and checks the denylist.
- `root` and `targets` are signed by maintainers with a threshold (2 of N) using ECDSA P-256 keys
  (OMS does not list Ed25519). `snapshot` and `timestamp` are signed online by CI; timestamp expires
  after one day.
- Each target is an OMS v1.0 in-toto statement (`https://model_signing/signature/v1.0`,
  `method: files`, `hash_type: sha256`). Our extras (BEP 52 roots, magnets, mirrors, licence tier)
  go in TUF target custom metadata so the OMS payload stays schema-valid.
- The client pins the initial root in the binary and follows TUF root rotation.
- Published to GitHub Pages; anyone can mirror the directory.

### Consequences

- Good: standard, audited update semantics instead of a homemade scheme.
- Good: our manifest SHA-256 values equal the Hub's own, so the registry can be checked against the
  Hub while it still exists.
- Bad: key management work for maintainers. tuf-on-ci handles most of it through PRs.

## More information

Revisit if the OpenSSF model-signing project publishes a registry format of its own.
