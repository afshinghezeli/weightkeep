# weightkeep design

Status: living document. Last reviewed 2026-09-24.
Decisions that are hard to reverse live in [`docs/adr/`](adr/); this file explains how the pieces fit.

## The problem

Most people who run open-weight models locally depend on one host. transformers, vLLM, diffusers,
llama.cpp's `-hf` flag and Ollama's `hf.co/...` names all resolve against huggingface.co at runtime.
That works until it doesn't:

- Orgs delete their own repos. Runway removed `runwayml/stable-diffusion-v1-5` in August 2024 and broke
  defaults across diffusers. Microsoft pulled WizardLM-2 hours after release. TRELLIS weights started
  returning 404 in May 2025.
- Deleted namespaces can be re-registered by someone else (Unit 42, "model namespace reuse", 2025).
  The same `org/name` then serves different bytes.
- Repos get gated after the fact, geo-restricted (Llama 3.2 vision in the EU), or silently edited on `main`.
- The Hub is changing owner. NVIDIA signed an agreement to acquire Hugging Face on 2026-09-02. Its own
  8-K says governments "may impose new or additional requirements" that could "restrict the models or
  datasets available through Hugging Face".

None of this means the Hub is going away. It means a pipeline that needs `org/model@commit` in two years
should not depend on a single URL still answering.

## What weightkeep is

A single Go binary that keeps verified copies of the model revisions you depend on, and serves them back
through the same HTTP API the Hub speaks.

- **HF first, never HF only.** The Hub stays the primary source. Other sources (a BitTorrent swarm, HTTP
  mirrors, IPFS gateways, other weightkeep nodes) are used when the Hub fails or when they are faster.
- **Bytes are checked, hosts are not trusted.** Every file is pinned by commit and verified against a
  SHA-256 recorded when it was first fetched (or published in a signed registry manifest). Where the
  bytes came from does not matter once the hash matches.
- **Zero workflow change.** `weightkeep serve` answers the Hub API on localhost. Point `HF_ENDPOINT`
  (or llama.cpp's `MODEL_ENDPOINT`) at it and existing tools keep working, online or off.
- **Licence-aware sharing.** Anyone can keep a private copy of anything they can download. Sharing
  (seeding) is limited to models whose licence allows redistribution; gated repos are never shared.

## What it is not

- Not a catalogue of models, and not a way to obtain models that were removed for legal reasons.
  The registry indexes hashes of openly licensed revisions and keeps a denylist.
- Not a Hub replacement. No uploads, no model cards, no Spaces.
- Not a downloader race. It is not trying to beat `hf download` on a fast link; it is trying to still
  work when `hf download` can't.

## Commands (target surface for 1.0)

| Command | What it does |
| --- | --- |
| `weightkeep pull org/model[@rev]` | Resolve `rev` to a commit, download every file into the store, verify, record a manifest. `--include`/`--exclude` globs for picking one quant. |
| `weightkeep ls` | List kept revisions, sizes, licence tier, verification age. |
| `weightkeep verify [org/model[@rev]]` | Re-hash stored blobs (bitrot check). |
| `weightkeep serve` | HF-compatible HTTP endpoint. Pull-through on cache miss when online. |
| `weightkeep export org/model@rev` | Materialise a revision into the standard HF cache layout (or a plain directory). |
| `weightkeep seed` | Share kept blobs over BitTorrent, within disk and bandwidth budgets, for licences that allow it. |
| `weightkeep registry sync` | Fetch the signed community registry (TUF). |
| `weightkeep gc` | Drop unreferenced blobs. |
| `weightkeep env` | Print resolved paths, upstream and token source (never the token). |

## Architecture

```
            HF_ENDPOINT=http://127.0.0.1:8700
 transformers / vLLM / llama.cpp / hf CLI
                     │
                     ▼
            ┌──────────────────┐        ┌───────────────┐
            │  proxy (serve)   │◄──────►│   manifests   │  commit → files → sha256
            └────────┬─────────┘        └───────────────┘
                     │ miss                     ▲
                     ▼                          │
            ┌──────────────────┐        ┌───────┴───────┐
            │  fetch scheduler │───────►│     store     │  blobs/sha256/.. + SQLite
            └──┬─────┬─────┬───┘        └───────────────┘
               │     │     │
           Hub API  swarm  mirrors (HTTP, IPFS gateways)
```

### Packages

| Package | Responsibility |
| --- | --- |
| `cmd/weightkeep` | `main`, wiring only. |
| `internal/cli` | Cobra commands, flag parsing, output formatting. No business logic. |
| `internal/config` | Paths, config file, environment overrides. |
| `internal/hub` | Client for the upstream Hub API: revision info, tree, resolve metadata, file bytes. |
| `internal/store` | Content-addressed blob store and SQLite metadata. The only package that writes under `blobs/`. |
| `internal/manifest` | Per-revision manifest type, canonical JSON, (later) OMS signing. |
| `internal/fetch` | Download a file from one or more sources into the store, with resume and verification. |
| `internal/hfcache` | Write the `models--org--name/{blobs,snapshots,refs}` layout used by huggingface_hub and llama.cpp. |
| `internal/proxy` | The HF-compatible HTTP server. |
| `internal/policy` | Licence tiers and the rules for what may be shared. |
| `internal/torrent` | Hybrid v1/v2 torrent builder and the anacrolix client wrapper. (M3) |
| `internal/registry` | TUF client for the community registry. (M4) |

Dependency direction is strictly downward in that table: `cli` may import anything, `store` imports
nothing from this repo except small helpers. `proxy` never calls the Hub directly; it goes through
`fetch` and `hub`.

### Store

```
$WEIGHTKEEP_HOME/                       default: $XDG_DATA_HOME/weightkeep or ~/.local/share/weightkeep
  blobs/sha256/ab/abcdef…               immutable, mode 0444, named by SHA-256 of the content
  tmp/                                  in-progress downloads (same filesystem, so rename is atomic)
  manifests/models/org/name/<commit>.json
  weightkeep.db                         SQLite (WAL): blobs, revisions, files, refs, sources
```

- Key is SHA-256 of the content. For LFS/Xet files this equals the Hub's `X-Linked-Etag` and `lfs.oid`.
  Small git files have no SHA-256 in any Hub API, so we compute it and keep the git blob SHA-1 alongside
  (the Hub's ETag for those files is the SHA-1, and clients name their cache blobs after the ETag).
- A blob is written to `tmp/`, hashed while streaming, fsynced, then renamed into place. Nothing under
  `blobs/` is ever partially written.
- Revisions are always stored by full commit SHA. Branch names are a `refs` table entry with a fetch time.
- Details and the reasoning: [ADR 0003](adr/0003-content-addressed-store.md).

### Proxy

The proxy imitates the Hub closely enough that `huggingface_hub` cannot tell the difference. The full
wire-level notes are in `.claude/skills/hf-protocol/reference.md`; the rules that matter most:

1. Every `/resolve/` response, including redirects and 404s, carries `X-Repo-Commit`.
2. `ETag` and `X-Linked-Etag` are the git SHA-1 for regular files and the SHA-256 for LFS files, and the
   tree listing's `oid`/`lfs.oid` match them. llama.cpp and huggingface_hub name cache files after these.
3. Every Xet signal is removed: `X-Xet-*` headers, `Link: rel="xet-auth"`, and `xetHash` in JSON.
   Otherwise newer clients bypass the proxy and talk to Xet CAS directly.
4. Upstream failures become 502/504, never 401 or `RepoNotFound`, which clients treat as final.
5. `/api/models/{repo}/refs` exists (current llama.cpp needs it) and tree listings are returned in one
   page unless the client asks for a `limit`.

Route priority for the MVP is in [ADR 0004](adr/0004-hub-compatible-proxy.md).

### Sources and fallback (M3)

A file is identified by SHA-256 and size. The fetch scheduler asks each source for byte ranges:

1. Local store.
2. The Hub (`/resolve/<commit>/<path>`, following the CDN redirect, never caching the signed URL).
3. Peers in the BitTorrent swarm for that revision.
4. Configured mirrors: plain HTTP bases and IPFS gateways.

Ranges are aligned to torrent pieces so every piece can be checked against the v2 piece layer before it
is written, whichever source it came from. The final SHA-256 over the whole file is the acceptance test.

Torrents are hybrid v1+v2, one per revision, named after the commit SHA, with the Hub's
`/resolve/` base as a BEP 19 web seed. A third-party client like qBittorrent can therefore fetch from
peers and fall back to the Hub by itself. See [ADR 0005](adr/0005-bittorrent-v2-with-hub-webseeds.md).

### Trust (M4)

- Per revision, a manifest lists every file's path, size, SHA-256, and (M3+) BEP 52 root.
- The exported form is an OpenSSF Model Signing (OMS) v1.0 statement, so tools from the model-signing
  project can verify our output, and our SHA-256 values equal the Hub's own.
- The community registry is a git repo of submitted manifests. CI turns it into a static TUF repository
  (root and targets signed by maintainers with a threshold, snapshot and timestamp signed online), served
  from GitHub Pages and any number of untrusted mirrors.
- A signed denylist ships through the same TUF repository.

See [ADR 0006](adr/0006-registry-trust-model.md).

### Licence policy

Four tiers, decided from the licence at the pinned commit, never from the tag alone:

| Tier | Examples | Keep privately | Seed |
| --- | --- | --- | --- |
| A, permissive | Apache-2.0, MIT, BSD, CC-BY, CC0 | yes | by default |
| B1, conditions | Llama 3.x/4 community, Gemma ToU, OpenRAIL, NVIDIA OML | yes | opt-in per model, licence and notice files bundled |
| B2, non-commercial | CC-BY-NC, Mistral MRL/MNPL | yes | opt-in, operator attests non-commercial use |
| C, never | gated, private, unknown, denylisted | yes (if you can download it) | never |

Keeping a private copy of something you were allowed to download is always allowed; the tool does not
police that. It only refuses to *share*. See [ADR 0007](adr/0007-licence-tiers.md).

## Milestones

The task list with acceptance criteria is in [`docs/roadmap.md`](roadmap.md).

| Milestone | Outcome |
| --- | --- |
| M0 Foundation | Repo, CI, conventions, ADRs. |
| M1 Keep | `pull`, `ls`, `verify`, `export`. A pulled model loads offline with transformers. |
| M2 Serve | `serve` passes the huggingface_hub, llama.cpp and text-generation-webui compatibility tests. |
| M3 Share | Hybrid torrents with Hub web seeds, `seed`, swarm and mirror fallback. |
| M4 Trust | OMS manifests, TUF registry, licence policy enforced, denylist. |
| M5 Launch | Release pipeline, installers, docs, demo. 0.1.0 ships at the end of M2; 0.2.0 at M3. |

## Open questions

- Files over 50 GB: huggingface_hub refuses plain HTTP downloads above that size when it knows the size.
  Two workarounds exist (Xet passthrough, or a cross-host redirect that hides the size); neither is tested.
- Ollama has no endpoint setting. `ollama pull localhost:8700/org/repo:Q4_K_M --insecure` works but stores
  the model under that name. Whether a DNS-based mode is worth supporting is undecided.
- Hub rate limits for web seed traffic at swarm scale. Worth talking to Hugging Face before 0.2.
- Whether a CLI click-through satisfies the "pass on use restrictions as enforceable provisions" clauses
  in OpenRAIL-style licences. Needs a lawyer before B1 seeding ships.
