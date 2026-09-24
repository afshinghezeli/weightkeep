---
status: accepted
date: 2026-09-24
---

# Supply licence texts for permissively licensed repos without a LICENSE file

## Context and problem statement

ADR 0007 made "no licence file at the pinned commit" a hard rule for tier C. Checked against real
repos, that rule excludes most of what it was meant to allow: of five popular permissively licensed
repos (HuggingFaceTB/SmolLM2-135M, prajjwal1/bert-tiny, openai-community/gpt2,
bartowski/SmolLM2-135M-Instruct-GGUF, Qwen/Qwen3-0.6B), only Qwen3 has a LICENSE file. The others
declare `license: apache-2.0` or `mit` in the model card and nothing else. Apache-2.0 and MIT both
require passing a copy of the licence on to recipients, so sharing those repos verbatim, with no
licence text, doesn't meet the licence either.

## Decision drivers

- Tier A has to cover the common case, or seeding is pointless.
- Whatever we share must satisfy the licence's notice requirement.
- Custom licences (tier B) have texts we can't reliably reproduce.

## Considered options

1. Keep the hard rule. Most permissively licensed models become unshareable.
2. Trust the model card and share without licence text. Simple, and not compliant.
3. Ship the canonical texts of the tier A licences inside weightkeep and send the text along with
   anything shared for a repo that lacks its own LICENSE file.

## Decision outcome

Chosen option 3.

- A repo whose declared licence is tier A keeps tier A without a LICENSE file. weightkeep supplies the
  canonical SPDX text (Apache-2.0, MIT, BSD-2/3-Clause, ISC, CC0-1.0, CC-BY-4.0, CC-BY-SA-4.0 and the
  others in the tier A table) and attaches it to what it shares: in the torrent's metainfo under a
  `weightkeep.license` key and in the registry entry, and as `LICENSE.weightkeep` when exporting a
  shared revision. Torrent contents stay byte-identical to the repo, so Hub web seeds keep working.
- The attribution the model card gives (repo id, author, licence link) travels with the text.
- Tier B and B2 repos still need their own licence file; without it they are tier C.
- A LICENSE file that doesn't match the declared licence (checked by fingerprint phrases of each
  licence text) makes the repo tier C, as before.

The rest of ADR 0007 stands.

### Consequences

- Good: tier A covers the repos people actually use.
- Good: shared copies carry the licence text even when the upstream repo doesn't.
- Bad: weightkeep now carries licence texts (a few hundred KB) and has to keep them exact.
- Bad: a card that declares the wrong licence and has no LICENSE file to contradict it gets through.
  The registry's human review (ADR 0006) is the backstop.

## More information

Fingerprints and the tier table live in `internal/policy`, with a test per licence id.
