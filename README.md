# weightkeep

Keep verified copies of the open-weight models you depend on, and serve them from a local endpoint
that speaks the Hugging Face Hub API.

<!-- maintainer: write the opening paragraph here, in your own words. What happened to you that made
you build this, in two or three sentences. -->

```sh
weightkeep pull HuggingFaceTB/SmolLM2-135M
weightkeep serve &
export HF_ENDPOINT=http://127.0.0.1:8700
python -c "from transformers import AutoModelForCausalLM; AutoModelForCausalLM.from_pretrained('HuggingFaceTB/SmolLM2-135M')"
```

## Why

<!-- maintainer: write this section yourself. Facts you may want to use, all sourced in
docs/design.md: Runway deleted runwayml/stable-diffusion-v1-5 in August 2024 and broke diffusers
defaults; WizardLM-2 was pulled hours after release; deleted namespaces can be re-registered
(Unit 42, 2025); NVIDIA signed an agreement to acquire Hugging Face on 2026-09-02 and its 8-K says
governments may "restrict the models or datasets available through Hugging Face". Avoid: "ban",
"censorship", "takedown-proof". -->

## What it does

- **`pull`** pins a repository to a commit, downloads it into a content-addressed store, and checks
  every file against the hash the Hub lists for it. Interrupted downloads resume. Files shared
  between repos or revisions are stored once.
- **`serve`** answers the Hub API on localhost. transformers, vLLM, diffusers, the `hf` CLI,
  llama.cpp's `-hf`, Ollama and text-generation-webui load models through it without code changes.
  Files it doesn't have yet are fetched from the Hub on first use, verified, kept, and streamed to the
  client as they arrive. When the Hub is unreachable, everything already kept is still served.
- **`verify`** re-hashes what's kept and quarantines anything that changed on disk.
- **`export`** writes a kept model into the Hugging Face cache (or a plain directory), so
  `HF_HUB_OFFLINE=1` works without anything running.

Sharing kept models with other people over BitTorrent, with the Hub as a fallback seed, is the next
milestone ([roadmap](docs/roadmap.md)). It will only ever share models whose licence allows it.

## Install

Download a binary for Linux, macOS or Windows from the
[releases page](https://github.com/afshinghezeli/weightkeep/releases), or build it with Go 1.26+:

```sh
go install github.com/afshinghezeli/weightkeep/cmd/weightkeep@latest
```

Release archives come with a build provenance attestation:

```sh
gh attestation verify weightkeep_0.1.0_linux_amd64.tar.gz -R afshinghezeli/weightkeep
```

## Quick start

```sh
# Keep a model. The second run makes one request and downloads nothing.
weightkeep pull HuggingFaceTB/SmolLM2-135M

# Only one quantisation of a GGUF repo (small files like the licence are always kept).
weightkeep pull bartowski/SmolLM2-135M-Instruct-GGUF --include '*Q4_K_M*'

weightkeep ls
weightkeep verify

# Serve it.
weightkeep serve
```

Then, in another terminal:

```sh
export HF_ENDPOINT=http://127.0.0.1:8700      # huggingface_hub and everything built on it
export MODEL_ENDPOINT=http://127.0.0.1:8700/  # llama.cpp
llama-completion -hf bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M -p "The capital of France is"
ollama pull 127.0.0.1:8700/bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M --insecure
```

Setup notes for each client, including what isn't supported: [docs/clients.md](docs/clients.md).

## How it works

A pull resolves the branch to a commit and lists the repository's files with their hashes: SHA-256
for large (LFS) files, the git blob id for small ones. Each file is streamed into `tmp/`, hashed as it
arrives, checked against that list, then renamed into `blobs/sha256/<hash>`. A manifest records the
revision: every path, size and hash. It's written as canonical JSON so it can be signed later.

The endpoint serves those manifests and blobs in the exact shape the Hub uses: the same ETags, the same
`X-Repo-Commit` header, the same error codes, so clients can't tell the difference. The one deliberate
difference is Xet: weightkeep removes the signals that make newer clients fetch from Hugging Face's
storage servers directly, which would bypass the endpoint. When it streams a file it hasn't finished
verifying, it holds back the final byte until the hash checks out, so a client never ends up with a
complete file that doesn't match.

Design notes and the reasoning behind each decision: [docs/design.md](docs/design.md),
[docs/adr](docs/adr).

## Compared to

|  | weightkeep | `hf download` + HF cache | [olah](https://github.com/vtuber-plan/olah) | torrent indexes |
| --- | --- | --- | --- | --- |
| Works when the Hub is unreachable | kept models | cached models, with `HF_HUB_OFFLINE=1` | cached files | while seeded |
| Clients unchanged (`HF_ENDPOINT`) | yes | n/a | yes | no |
| llama.cpp `-hf` and Ollama | yes | no | llama.cpp: open [issue #85](https://github.com/vtuber-plan/olah/issues/85) | no |
| Checks content against Hub hashes | SHA-256 or git id, every file | size only over HTTP; Xet checks its chunks | not documented | varies |
| Detects later corruption on disk | `verify` | no | not documented | client recheck |
| Dedup | whole files, across repos | Xet chunks | not documented | no |
| P2P fallback | not yet | no | no | yes |

Checked against huggingface_hub 1.32 and olah's README and issues in September 2026.

## Status and limits

Version 0.1. It works for me on macOS and Linux with the clients listed above, and the compatibility
suite runs them against the real Hub on every change. Expect the command-line flags to change before 1.0;
the store format has a migration path.

- Files over 50 GB can't be served to huggingface_hub over plain HTTP (it refuses; the Hub itself uses
  Xet for those). Not solved yet.
- Only the most recent llama.cpp and Ollama releases are tested.
- `serve` has no authentication. It binds to localhost by default; if you open it up, anyone who can
  reach it can read every kept model, including gated ones you pulled with your own token.
- Datasets and Spaces aren't supported.

## FAQ

**Is this for getting around takedowns?** No. weightkeep keeps what you pull, for your own use, the same
way the Hugging Face cache does, with stronger checks. When sharing arrives it will be limited to models
whose licence allows redistribution, never gated repos, and it will honour a denylist.

**Why not just use the Hugging Face cache?** The cache is keyed per repo and branch, doesn't re-verify
files, and gets pruned. It also only helps tools that use huggingface_hub; llama.cpp and Ollama keep their
own copies. weightkeep is one verified store behind one endpoint for all of them.

**Does it work with gated models?** For your own copy, yes: set `HF_TOKEN` (or run `hf auth login`) after
accepting the model's terms on the Hub. weightkeep will never share them.

**Does it phone home?** No. The only network traffic goes to the upstream Hub you configure
(`weightkeep env` shows which).

**Where does it keep things?** `~/.local/share/weightkeep` by default (`%LOCALAPPDATA%\weightkeep` on
Windows). `weightkeep env` prints every path it uses.

## Contributing

Bug reports from real setups are the most useful thing right now. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

Apache License 2.0. Model weights are not part of this repository, and each model stays under its own
licence.
