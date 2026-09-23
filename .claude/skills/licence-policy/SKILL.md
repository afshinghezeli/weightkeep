---
name: licence-policy
description: Model licence tiers (A, B1, B2, C) and the rules for what weightkeep may seed or list in the registry. Use when working on internal/policy, seeding, registry submission checks, or any docs, tests or examples that name a specific model.
paths:
  - "internal/policy/**"
  - "internal/seed/**"
  - "internal/registry/**"
---

# Licence policy

Decision record: `docs/adr/0007-licence-tiers.md`. The licence-by-licence catalogue with sources and
the legal background is in [reference.md](reference.md). It is research, not legal advice; items marked
[UNCERTAIN] there need a lawyer before behaviour depends on them.

## Invariants (never relax without a new ADR)

- Gated (`gated: auto`, `gated: manual`, `true`) or private repos are tier C. Always.
- Missing licence file at the pinned commit is tier C, whatever the tag says.
- Licence text that doesn't match the declared tag is tier C.
- Denylist hit is tier C.
- A model inherits a stricter tier from its `base_model`.
- Unknown licence ids default to C.
- Tiers only restrict sharing (seed, registry). `pull` and `serve` for the user's own use are not
  restricted.

## Tiers

| Tier | Share | Examples |
| --- | --- | --- |
| A | by default, LICENSE bundled | apache-2.0, mit, bsd-*, isc, cc0-1.0, cc-by-4.0, cc-by-sa-4.0, openmdw, mpl-2.0 |
| B1 | opt-in per model; LICENSE, NOTICE, use policy bundled; verbatim only | llama3.x, llama4, gemma (1-3), openrail family, qwen licence, deepseek v1, falcon, nvidia-open-model-license, stabilityai-community, cc-by-nd-4.0 |
| B2 | as B1, plus operator attests non-commercial | cc-by-nc-*, mistral mrl/mnpl, flux-1-dev-nc, qwen-research |
| C | never | gated, private, unknown, missing, denylisted, research licences needing individual acceptance |

## In tests, docs and demos

Only tier A models, and small ones. Good defaults:

- `prajjwal1/bert-tiny` (MIT, 5 files, 18 MB): default for network tests
- `HuggingFaceTB/SmolLM2-135M` (Apache-2.0, 270 MB): realistic transformers example
- `bartowski/SmolLM2-135M-Instruct-GGUF` with `--include '*Q4_K_M*'` (Apache-2.0): llama.cpp and Ollama
- `openai-community/gpt2` (MIT): only with include filters, the full repo is several GB

Check before adding a new one: `curl -s https://huggingface.co/api/models/<id> | jq '.cardData.license, .gated'`.
Several popular test repos (`hf-internal-testing/tiny-random-*`, `sshleifer/tiny-gpt2`) declare no
licence at all, which makes them tier C.

Never name a gated model in an example command, even to show it being refused; describe it instead.

## Language

Say "open-weight", not "open source", for models. Never describe weightkeep as a way to get models that
were taken down. The framing is keeping and verifying what you already depend on.
