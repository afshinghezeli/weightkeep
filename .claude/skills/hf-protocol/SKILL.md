---
name: hf-protocol
description: Hugging Face Hub wire protocol as real clients (huggingface_hub, transformers, vLLM, llama.cpp, Ollama, text-generation-webui) use it. Use when writing or reviewing code in internal/proxy, internal/hub or internal/hfcache, when adding a proxy route, or when debugging why a client rejects a proxy response.
paths:
  - "internal/proxy/**"
  - "internal/hub/**"
  - "internal/hfcache/**"
  - "test/compat/**"
---

# Hub protocol

The full notes, with source line references into huggingface_hub, llama.cpp, Ollama and olah, are in
[reference.md](reference.md). Read the section you need before changing behaviour; don't guess header
semantics from memory.

## Rules that break clients when violated

1. `X-Repo-Commit: <40 hex>` on every `/resolve/` response: 200, 206, 3xx and 404. Without it
   huggingface_hub raises "does not seem to be served by a Hugging Face Hub endpoint".
2. `ETag` and `X-Linked-Etag` = quoted git blob SHA-1 for regular files, quoted SHA-256 for LFS files.
   Tree `oid` / `lfs.oid` must equal what resolve serves (llama.cpp and huggingface_hub name cache
   blobs after them).
3. Strip Xet everywhere: `X-Xet-Hash`, `X-Xet-Refresh-Route`, `Link` with `xet-auth` /
   `xet-reconstruction-info`, and `xetHash` in any JSON body. Stripping headers alone is not enough;
   huggingface_hub 1.x skips HEAD when the tree listing has `xetHash`.
4. Never pass through a `Location` to `*.cdn.hf.co` or `cas-server.xethub.hf.co`. Serve bytes directly.
5. Rewrite absolute `https://huggingface.co` URLs in `Location`, `Link` and JSON to our base URL.
6. Upstream timeout or 5xx: return 502/504. Never 401, never `X-Error-Code: RepoNotFound`.
7. Pass upstream 4xx through with `X-Error-Code` and `X-Error-Message` intact.
8. Range: `bytes=N-` and `bytes=a-b` return 206 with `Content-Range`; unsatisfiable returns 416 with
   `Content-Range: bytes */SIZE`. Zero-length files: `Content-Length: 0`, no `Content-Range`.
9. HEAD must be fast (client timeout is 10 s). Answer from metadata.
10. No `Content-Encoding` on file bodies.

## Error mapping

| Situation | Status | X-Error-Code |
| --- | --- | --- |
| Missing file at a known commit | 404 (+ `X-Repo-Commit`) | `EntryNotFound` |
| Unknown revision | 404 | `RevisionNotFound` |
| Unknown repo | 404 | `RepoNotFound` |
| Gated, no token | 401 | `GatedRepo` |
| Gated, token without access | 403 | `GatedRepo` |
| Upstream down, not in store | 504 | none |

## Endpoint priorities

- P0: `HEAD/GET /{repo}/resolve/{rev}/{path}`, `GET /api/models/{repo}[/revision/{rev}]`,
  `GET /api/models/{repo}/tree/{rev}[/{path}]`.
- P1: `/api/models/{repo}/refs`, `POST /paths-info/{rev}`, `/api/whoami-v2`, Ollama `/v2/` routes.
- Repo type prefixes: `datasets/`, `spaces/` on resolve; `/api/datasets/`, `/api/spaces/` on the API.
- Revisions arrive URL-encoded (`refs%2Fpr%2F1`); decode before routing.

## Checking your work

After any proxy change, run the compatibility suite (`make compat`) and at minimum:

```sh
HF_ENDPOINT=http://127.0.0.1:8700 uv run --with huggingface_hub python -c \
  "from huggingface_hub import snapshot_download; print(snapshot_download('prajjwal1/bert-tiny'))"
```

with `hf_xet` installed, and confirm the proxy log shows no `xet-read-token` requests.
