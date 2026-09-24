---
status: accepted (amended by 0009)
date: 2026-09-24
---

# Licence tiers decide what may be shared

## Context and problem statement

Keeping a private copy of a model you downloaded is ordinary. Redistributing it is governed by the
model's licence, and for gated repos by a click-through agreement with the author. A torrent index
that ignores this is legally exposed (Columbia Pictures v. Fung, 2013) and would lose the trust of the
people we most want to use it.

The Hub's `license:` tag is self-declared and often wrong or missing.

## Decision drivers

- Never redistribute gated, private, or unlicensed content.
- Never let the tag alone decide.
- Keep the default path useful: most popular open models are Apache-2.0 or MIT.

## Decision outcome

Four tiers, computed per revision:

- **A** (Apache-2.0, MIT, BSD, ISC, CC0, CC-BY, CC-BY-SA, OpenMDW, MPL-2.0 and similar):
  seeding allowed by default. The LICENSE file travels with the content.
- **B1** (Llama community licences, Gemma ToU, OpenRAIL family, Qwen, DeepSeek v1, TII Falcon,
  NVIDIA OML, Stability Community, CC-BY-ND): seeding only after explicit opt-in per model; LICENSE,
  NOTICE and use-policy files must be present; bytes are shared verbatim.
- **B2** (CC-BY-NC family, Mistral MRL/MNPL, FLUX dev, Qwen research): as B1, plus the operator attests
  non-commercial use.
- **C** (gated at any level, private, `unknown`, missing, `other` without a recognised licence file,
  denylisted): never seeded.

Hard rules checked before the licence lookup: gated or private means C; denylist hit means C; no licence
file at the pinned commit means C; a licence file whose text does not match the declared tag means C;
a model whose `base_model` has a stricter tier inherits that tier.

Keeping and serving locally (`pull`, `serve`) is not restricted by tier.

### Consequences

- Good: the default behaviour is defensible and easy to explain.
- Good: tests, docs and demos only ever use tier A models (avoids the youtube-dl test fixture problem).
- Bad: some models people care about will be tier C. That is correct.

## More information

The policy lives in `internal/policy` as data, with a table-driven test per licence id. Changes need
two maintainer approvals once there are two maintainers.
