---
status: accepted
date: 2026-09-24
---

# Imitate the Hub API, strip Xet

## Context and problem statement

The point of `weightkeep serve` is that nothing else changes: `HF_ENDPOINT=http://127.0.0.1:8700`
and transformers, vLLM, diffusers, `hf download`, text-generation-webui and llama.cpp all keep working.
That requires answering the subset of the Hub API those clients use, byte-for-byte where it matters.

Since 2025 the Hub stores large files in Xet. Clients that see Xet metadata skip `HF_ENDPOINT` for the
actual bytes and talk to `cas-server.xethub.hf.co` directly, which defeats a local endpoint.

## Decision drivers

- huggingface_hub's exact header expectations (`X-Repo-Commit` on every resolve response, ETag
  semantics, error codes that decide whether it falls back to cache).
- llama.cpp (March 2026 and later) needs `/refs` and an unpaginated `/tree`.
- Known failure modes of existing mirrors (olah issues #52, #58, #62, #77, #85).

## Considered options

1. Transparent reverse proxy that forwards everything and caches bodies.
2. Implement the endpoints clients use, serve file bytes from our store, pass other API calls through.
3. Implement the Xet protocol ourselves.

## Decision outcome

Chosen option 2. File bytes always come from our store (fetched on miss); API calls we do not
implement pass through to the upstream when online.

Rules:

1. Every `HEAD`/`GET /{repo}/resolve/{rev}/{path}` response carries `X-Repo-Commit`, including 404.
2. `ETag` and `X-Linked-Etag` are the quoted git SHA-1 for regular files and SHA-256 for LFS files.
   Tree `oid` / `lfs.oid` match them.
3. Remove `X-Xet-Hash`, `X-Xet-Refresh-Route`, `Link` entries with `rel="xet-auth"` or
   `rel="xet-reconstruction-info"`, and every `xetHash` field in JSON.
4. Never forward a `Location` pointing at a CDN or CAS host. Serve bytes with 200/206 directly.
5. Rewrite absolute upstream URLs in `Location`, `Link` and JSON to our own base URL.
6. Upstream timeouts and 5xx map to 502/504. Upstream 4xx pass through with `X-Error-Code` intact.
   We never synthesise 401 or `RepoNotFound` for a transport problem.
7. Zero-length files: `Content-Length: 0`, no `Content-Range`.

Route priority:

- P0 (0.1.0): resolve HEAD/GET with Range; `/api/models/{repo}` and `/revision/{rev}`; `/tree`;
  error semantics.
- P1: `/refs`, `/paths-info`, `/api/whoami-v2`, Ollama `/v2/.../manifests` and `/v2/.../blobs`.
- P2: `xet-read-token` passthrough, commits, `refs/pr/N`, pinning config.

### Consequences

- Good: clients keep working unchanged, including ones with `hf_xet` installed.
- Good: offline mode is just "the store has it".
- Bad: we track the Hub's API as it changes. Mitigation: a compatibility test suite that runs real
  client libraries against the proxy in CI.
- Bad: files over 50 GB cannot be served over plain HTTP to huggingface_hub when it knows the size.
  Open question, see design doc.

## More information

The full wire notes, with source references, are in `.claude/skills/hf-protocol/reference.md`.
