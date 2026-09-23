# Research 02 — Hugging Face Hub wire protocol for a drop-in local proxy

Status: research spec, written 2026-09-24. Verified against live `huggingface.co` responses captured on
2026-09-23 and against source code at these versions:

| Component | Version / ref inspected |
|---|---|
| `huggingface_hub` | `main` = 1.33.0.dev0 (PyPI latest 1.32.0); v0.36.0 checked for legacy behaviour |
| `llama.cpp` | `master` (Sep 2026) plus commit `9e118b97` (2026-02-15, before the HF-cache rewrite) |
| `ollama` | `main` (Sep 2026): `server/images.go`, `server/download.go`, `types/model/name.go` |
| `olah` | `vtuber-plan/olah` `main` (last push 2026-08-31), including its issues |
| `text-generation-webui` | `main` `download-model.py` |
| `vllm`, `diffusers`, `transformers` | `main`, only the parts that touch the Hub |

Paths such as `file_download.py:1617` point to
`huggingface_hub/src/huggingface_hub/<file>` on `main` unless noted otherwise. Line numbers drift between releases.
Grep for the function name if a number no longer matches.

---

## 0. TL;DR design rules (read first)

1. **Every `/resolve/` response, HEAD and GET, including 3xx and 404, must carry `X-Repo-Commit: <40-hex sha>`.**
   Without it, huggingface_hub raises `FileMetadataError` ("does not seem to be served by a Hugging Face Hub endpoint")
   (`file_download.py:1788-1794`).
2. **The ETag value is the name of the client's cache blob.** Use the quoted git blob SHA-1 for regular git files and the
   quoted SHA-256 for LFS/Xet files. Send it as `X-Linked-Etag` (preferred) and/or `ETag`. This matches `huggingface.co`, so
   blobs dedupe with caches that were filled directly from HF.
3. **Force plain HTTP by removing every Xet signal**: the `X-Xet-Hash`, `X-Xet-Refresh-Route` and `Link: …rel="xet-auth"`
   headers on resolve responses, and the `xetHash` field in `/tree` and `/paths-info` JSON. `huggingface_hub` ≥1.x has a
   tree-cache fast path that skips HEAD and goes straight to Xet whenever `xetHash` is present (`file_download.py:1882-1928`).
4. **Never pass through upstream `Location:` URLs that point at `*.cdn.hf.co` or `cas-server.xethub.hf.co`.** The client
   would download straight from HF and bypass your store. Redirect to your own host, or serve with 200 directly.
5. **Answer HEAD from metadata, fast.** The default `HF_HUB_ETAG_TIMEOUT` is 10 s (`constants.py:37,310`). If HEAD is slow,
   clients silently fall back to cache or fail.
6. **Support `Range: bytes=N-`** (resume, returns 206) and arbitrary ranges (safetensors header probe
   `bytes=0-100000`, Ollama's 16 parallel parts, hf_transfer). Never gzip file bodies.
7. **Implement `/api/{type}s/{repo}/refs`.** Current llama.cpp resolves the commit through it, and olah's missing `/refs`
   is exactly why "olah doesn't work with llama.cpp" (olah issue #85).
8. **Tree pagination must terminate.** Either return everything in one page with no `Link` header, or honour `cursor`.
   text-generation-webui builds its own cursor and loops until it gets `[]`. If you ignore `cursor`, it loops forever.
9. **Don't turn transport errors into 401 or 404.** huggingface_hub maps 401 and `RepoNotFound` to non-retryable
   `RepositoryNotFoundError`. Use 502/503/504 for upstream failures (olah PRs #62 and #77).
10. **Files larger than 50 GB** cannot be fetched by huggingface_hub over plain HTTP when it knows the size
    (`MAX_HTTP_DOWNLOAD_SIZE`, `constants.py:41`, `file_download.py:379-385`). See §3.6 for the workaround.

---

## 1. Endpoints clients hit

### 1.0 URL grammar and routing

```
ENDPOINT = $HF_ENDPOINT (default https://huggingface.co), trailing "/" stripped      (constants.py:69)
repo_type URL prefix: model -> "" ; dataset -> "datasets/" ; space -> "spaces/" ; kernel -> "kernels/"   (constants.py:123-127)
API type segment:      models | datasets | spaces | kernels   (f"{repo_type}s")
```

* **repo_id** is either `namespace/name` or a legacy single segment (`gpt2`, `bert-base-uncased`). Routes must accept both.
  HF canonicalises legacy and case-mismatched ids with a **relative 307**. For example, `GET /api/models/gpt2` returns
  `307 Location: /api/models/openai-community/gpt2`. olah needed a fix for case-mismatched names (PR #57).
* **revision** is URL-encoded with `quote(revision, safe="")` in both resolve and API URLs (`file_download.py:hf_hub_url`,
  `hf_api.py:3313, 4101, 4401`). `refs/pr/1` therefore arrives as `refs%2Fpr%2F1`. Decode it before routing. Tolerate an
  unencoded `refs/pr/N` from hand-written clients. HF accepts short shas: `resolve/607a30d/...` returns 307 with the full
  `x-repo-commit`.
* **filename** is encoded with `quote(filename)` (the default `safe="/"`), so `/` inside paths stays literal.
* The `/tree/{rev}/{path}` path argument is encoded with `quote(path, safe="")` (`hf_api.py:4101`), so `/` becomes `%2F`
  there. Decode it.
* Parsing rule for `/resolve/`: split on the first `/resolve/` segment. An optional leading `datasets|spaces|kernels`
  segment sets the type, the rest is `repo_id` (1 or 2 segments), then comes `{rev}` (one decoded segment, or
  `refs/pr/N`, or `refs/convert/...`), then `{path…}`.

### 1.1 File download: `HEAD|GET /{prefix}{repo_id}/resolve/{revision}/{path}`

Used by `hf_hub_download` / `snapshot_download` (`file_download.py:1617-1690`), transformers, diffusers, vLLM,
llama.cpp (HEAD then GET, following redirects), text-generation-webui, and `hf download`.

**What huggingface_hub does** (`get_hf_file_metadata`, `file_download.py:1617`; `_httpx_follow_hub_redirects_with_backoff`,
`utils/_http.py:725-774`):

1. Sends `HEAD` with `Accept-Encoding: identity`, `authorization: Bearer <token>` (if any), `user-agent`, and
   `follow_redirects=False`.
2. Follows 3xx manually only while the target host equals the request host or is in `HF_URL_HOSTS`
   (huggingface.co, hub-ci, and the host of `HF_ENDPOINT`) (`_is_same_or_hub_host`, `_http.py:777-780`). It stops at the
   first redirect to any other host (a CDN) and **reads metadata from that 3xx response**.
3. Reads these values from the final response it stopped at:
   * `commit_hash = headers["X-Repo-Commit"]` (**required**)
   * `etag = normalize(headers["X-Linked-Etag"] or headers["ETag"])`. `_normalize_etag` does
     `etag.lstrip("W/").strip('"')` (`file_download.py:606-625`), so quoted, unquoted and weak forms all work. Keep hex
     lowercase.
   * `size = int(X-Linked-Size or (None if is_redirect else Content-Length))`
   * `location = headers["Location"] or request.url`. This URL is then fetched with GET. Relative Locations are resolved
     with `urljoin`.
   * `xet_file_data`: see §3.
4. If `location` is on a different, non-Hub host, it drops `authorization` before the GET (`file_download.py:1815-1821`).
5. `GET location` with `Range: bytes=N-` on resume. If it gets 200 instead of 206, it truncates and restarts
   (`file_download.py:400-417`). The byte count must equal `expected_size` or it raises "Consistency check failed"
   (`file_download.py:488-494`).
6. On 404 with `X-Error-Code: EntryNotFound` and an `X-Repo-Commit` header, it writes `.no_exist/<commit>/<path>` into the
   cache (`file_download.py:1767-1784`). HF does include `x-repo-commit` on EntryNotFound 404s (verified). Mirror that.

**Live HF responses (2026-09-23), for reference:**

Regular git file (`openai-community/gpt2/config.json`):
```
HEAD /openai-community/gpt2/resolve/main/config.json
HTTP/2 307
location: /api/resolve-cache/models/openai-community/gpt2/607a30d783dfa663caf39e06633721c8d4cfcd7e/config.json?%2Fopenai-community%2Fgpt2%2Fresolve%2Fmain%2Fconfig.json=&etag=%2210c66461e4c109db5a2196bff4bb59be30396ed8%22
x-repo-commit: 607a30d783dfa663caf39e06633721c8d4cfcd7e
x-linked-etag: "10c66461e4c109db5a2196bff4bb59be30396ed8"      <- git blob sha1
accept-ranges: bytes
content-disposition: inline; filename*=UTF-8''config.json; filename="config.json";
access-control-expose-headers: X-Repo-Commit,X-Request-Id,X-Error-Code,X-Error-Message,X-Total-Count,ETag,Link,Accept-Ranges,Content-Range,X-Linked-Size,X-Linked-ETag,X-Xet-Hash

(followed, same host)
HEAD /api/resolve-cache/models/.../config.json?...
HTTP/2 200
content-length: 665
etag: "10c66461e4c109db5a2196bff4bb59be30396ed8"
x-repo-commit: 607a30d783dfa663caf39e06633721c8d4cfcd7e
accept-ranges: bytes
content-type: text/plain; charset=utf-8
(If-None-Match with the same etag -> 304)
```
Note: HF now uses a **relative 307** to a same-host `/api/resolve-cache/...` URL for small files. olah issue #52 broke on
this because it did not follow relative redirects.

LFS/Xet file (`model.safetensors`, 548 MB):
```
HEAD /openai-community/gpt2/resolve/main/model.safetensors
HTTP/2 302                 (307 for Ollama's User-Agent)
location: https://us.aws.cdn.hf.co/xet-bridge-us/621ffdc0.../63bed808...?X-Xet-Cas-Uid=public&...&Signature=...
x-repo-commit: 607a30d783dfa663caf39e06633721c8d4cfcd7e
x-linked-size: 548105171
x-linked-etag: "248dfc3911869ec493c76e65bf2fcf7f615828b0254c12b473182f0f81d3a707"   <- sha256 of content (= lfs oid)
x-xet-hash: 63bed80836ee0758c8fd4f8975d59bb0b864263ee2753547c358e8a37cde8758
link: <https://huggingface.co/api/models/openai-community/gpt2/xet-read-token/607a30d7...>; rel="xet-auth", <https://cas-server.xethub.hf.co/v1/reconstructions/63bed808...>; rel="xet-reconstruction-info"
accept-ranges: bytes
cache-control: no-store
content-length: 1008        <- length of the redirect body, NOT the file (hence X-Linked-Size)

GET <cdn url>  Range: bytes=0-99
HTTP/2 206
etag: "63bed808..."        <- CDN etag = xet hash, NOT sha256: this is why clients prefer X-Linked-Etag
content-range: bytes 0-99/548105171
accept-ranges: bytes
```

**What the proxy should return.** Pick one of two models.

*Model A, serve directly (simplest, recommended for the MVP):*
```
HEAD|GET /{repo}/resolve/{rev}/{path}
200 (GET with Range -> 206 + Content-Range; bad range -> 416 + Content-Range: bytes */SIZE)
X-Repo-Commit: <40-hex commit resolved from rev>
ETag: "<sha256 if LFS else git-sha1>"
X-Linked-Etag: "<same>"
X-Linked-Size: <size>             (optional with 200; harmless)
Content-Length: <size or range length>
Accept-Ranges: bytes
Content-Type: application/octet-stream
Content-Disposition: inline; filename*=UTF-8''<basename>; filename="<basename>";
(no Content-Encoding, no X-Xet-*, no Link xet headers)
```

*Model B, HF-like redirect:* `HEAD` returns `307 Location: /blobs/sha256/<hex>` (relative, same host) plus
`X-Repo-Commit`, `X-Linked-Etag`, `X-Linked-Size`. The client follows it because the host is the same and keeps
`authorization`. This model lets many repos and revisions share one content-addressed URL. Use 307, not 302: Ollama's
direct-URL probe accepts only 307 or 200 (`server/download.go:266`), and huggingface_hub treats any 3xx with `Location`
as a redirect.

Etag rules:
* Regular file in git: `oid` = git blob SHA-1 = `sha1(b"blob " + str(len).encode() + b"\0" + content)`. You can compute it
  locally for self-hosted content.
* LFS or Xet file: SHA-256 of the full content, which equals `lfs.oid` / `lfs.sha256` in the APIs.
* `_hf_hub_download_to_local_dir` treats a 64-hex etag as a sha256 and verifies existing local files against it
  (`file_download.py:1444-1452`). A wrong value causes re-downloads.

### 1.2 Repo info: `GET /api/{models|datasets|spaces}/{repo_id}[/revision/{rev}]`

Built in `hf_api.py:3309-3325` (`model_info`), `3380` (`dataset_info`), and `3520` (`space_info`). Query params:
`blobs=true` (from `files_metadata=True`), `securityStatus=true`, `expand=<field>` (repeatable `expand[]=`).

Who uses it:
* `snapshot_download` calls `repo_info(revision=...)` only to resolve `sha` (the assert at `_snapshot_download.py:277-279`).
  This happens unless the revision is already a 40-hex sha.
* huggingface_hub ≤0.36 `snapshot_download` enumerates files from `siblings` (v0.36 `_snapshot_download.py:258-262`) and
  falls back to `/tree` only when the repo has more than 50 000 files.
* diffusers `DiffusionPipeline.download` reads `info.siblings[].rfilename` and `info.sha`
  (`pipelines/pipeline_utils.py:1619-1635`).
* vLLM `resolve_revision`, `repo_exists`, `revision_exists`, `HfApi.model_info`.

Minimum response (the `ModelInfo.__init__` required key is only `id`; everything else is `pop(..., None)`,
`hf_api.py:~990-1060`):
```json
{
  "_id": "621ffdc036468d709f17434d",
  "id": "openai-community/gpt2",
  "modelId": "openai-community/gpt2",
  "author": "openai-community",
  "sha": "607a30d783dfa663caf39e06633721c8d4cfcd7e",
  "lastModified": "2024-02-19T10:57:45.000Z",
  "createdAt": "2022-03-02T23:29:04.000Z",
  "private": false,
  "gated": false,                        // or "auto" | "manual"
  "disabled": false,
  "tags": ["transformers","safetensors"],
  "pipeline_tag": "text-generation",
  "library_name": "transformers",
  "siblings": [
    {"rfilename": "config.json"},
    {"rfilename": "model.safetensors"}
  ],
  "cardData": {}, "config": {}, "transformersInfo": {}, "safetensors": {"parameters": {"F32": 137022720}, "total": 137022720},
  "usedStorage": 11977009063, "downloads": 0, "likes": 0, "spaces": []
}
```
With `?blobs=true`, siblings gain `blobId`, `size` and `lfs`. **Note the key is `lfs.sha256` here, but `lfs.oid` in `/tree`:**
```json
{"rfilename":"config.json","blobId":"10c66461e4c109db5a2196bff4bb59be30396ed8","size":665},
{"rfilename":"model.safetensors","blobId":"44b36d6e32d13c8fb28b0feab0ac8bfefa7efeda","size":548105171,
 "lfs":{"sha256":"248dfc39...a707","size":548105171,"pointerSize":134}}
```
(`blobId` for an LFS file is the SHA-1 of the *pointer* file, not of the content.)

Passthrough recommendation: when online, proxy the upstream JSON verbatim and then rewrite it (strip `xetHash` wherever
it appears; keep `sha`). When offline, synthesize this minimal shape from the stored manifest. `safetensors`, `cardData`,
`config` and `transformersInfo` can be omitted offline, but transformers/vLLM occasionally read `safetensors.total` and
`config`, so cache the upstream body when you can.

`/api/models/{id}` without `/revision/` implies `main` (the default branch).

### 1.3 Tree listing: `GET /api/{type}s/{repo_id}/tree/{rev}[/{path}]?recursive=<bool>&expand=<bool>[&limit=N&cursor=...]`

Code: `list_repo_tree`, `hf_api.py:4098-4104`, using `paginate` in `utils/_pagination.py`, which follows `Link: <...>; rel="next"`.

Who uses it: `snapshot_download` in huggingface_hub 1.x (`_snapshot_download.py:393-407`, `recursive=True`, at the
**commit sha**), `list_repo_files`, `HfFileSystem` (vLLM), llama.cpp (`api/models/{repo}/tree/{commit}?recursive=true`,
`common/hf-cache.cpp:316`), text-generation-webui (non-recursive, with its own cursor), and transformers
(`utils/hub.py:150`).

Entry shapes (live):
```json
{"type":"directory","oid":"d03ec5ec179df58241d27d55f92a674f5f44197f","size":0,"path":"onnx"}
{"type":"file","oid":"10c66461e4c109db5a2196bff4bb59be30396ed8","size":665,"path":"config.json"}
{"type":"file","oid":"44b36d6e32d13c8fb28b0feab0ac8bfefa7efeda","size":548105171,
 "lfs":{"oid":"248dfc3911869ec493c76e65bf2fcf7f615828b0254c12b473182f0f81d3a707","size":548105171,"pointerSize":134},
 "xetHash":"63bed80836ee0758c8fd4f8975d59bb0b864263ee2753547c358e8a37cde8758",
 "path":"model.safetensors"}
```
* `recursive=true` includes `directory` entries as well as files. `path` is always relative to the repo root, even when a
  subpath was requested.
* `expand=true` adds `lastCommit: {id,title,date}` and `securityFileStatus: {status, avScan, pickleImportScan, ...}`
  (parsed at `hf_api.py:815-843`; `RepoFile` needs `securityFileStatus.status/avScan/pickleImportScan` if present). With
  expand, HF pages at 50 entries instead of 1000.
* `RepoFile` requires `path`, `size`, `oid` (`hf_api.py:816-818`). `lfs` needs `oid`, `size`, `pointerSize`
  (`hf_api.py:819-821`).
* The requested subpath is itself a file → HF returns 404 (e.g. `tree/main/onnx%2Fdecoder_model.onnx` gives 404).
* Pagination (live): `link: <https://huggingface.co/api/datasets/allenai/c4/tree/main/en?expand=false&recursive=true&limit=1000&cursor=ZXlK...%3D%3D>; rel="next"`.
  The cursor is `base64( base64(json{"file_name":..., "tree_oid":...}) + ":" + limit )`.
  * **Rewrite absolute upstream `Link` URLs to your own host.** `paginate` follows the URL verbatim
    (`_pagination.py`), so an unrewritten link sends page 2 straight to huggingface.co.
  * text-generation-webui (`download-model.py:81-154`) ignores `Link`. It builds
    `cursor = b64(b64('{"file_name":"<last path>"}') + b':50')` with `=` replaced by `%3D` and stops only on an empty array.
    Decode the cursor, return entries strictly after `file_name` in your ordering, and return `[]` past the end.
  * llama.cpp (`hf-cache.cpp:316`) ignores pagination and only reads page 1. Returning the whole tree unpaginated when no
    `limit` is given is the most compatible choice.
* In 1.x, huggingface_hub caches this listing forever at `<cache>/models--o--n/trees/<commit>.json` (`_tree_cache.py`), with
  keys `size`, `blob_id`, `lfs_sha256`, `lfs_size`, `xet_hash`. Wrong data poisons the client's cache for that commit.

### 1.4 Paths info: `POST /api/{type}s/{repo_id}/paths-info/{rev}`

Body: form-encoded `paths=a&paths=b&expand=false`. huggingface_hub sends `data={"paths":[...], "expand": bool}`
(`hf_api.py:4402-4408`). Accept JSON too (`{"paths":[...],"expand":false}`, which HF accepts). The response is an array
with the same entry shape as `/tree`, and missing paths are simply omitted. Used by `get_paths_info`, `HfFileSystem`,
`file_exists`-style checks, and uploads. olah has a dedicated route for it (`server_api_routes.py:473-517`).

### 1.5 Refs: `GET /api/{type}s/{repo_id}/refs[?include_prs=1]`

```json
{"tags":[],"branches":[{"name":"main","ref":"refs/heads/main","targetCommit":"607a30d7..."}],"converts":[],
 "pullRequests":[{"name":"187","ref":"refs/pr/187","targetCommit":"260cee1f..."}]}   // only with include_prs=1
```
**Required by current llama.cpp.** `get_repo_commit` (`common/hf-cache.cpp:234-290`) takes `branches[name=="main"].targetCommit`
(or the first branch), writes it to `refs/main`, and then lists the tree at that commit. If `/refs` fails, `-hf` fails with
"failed to resolve commit" (olah issue #85). Also used by `list_repo_refs` (`hf_api.py:4234-4245`).

### 1.6 Other API endpoints worth implementing or passing through

| Endpoint | Used by | MVP handling |
|---|---|---|
| `GET /api/whoami-v2` | `hf auth whoami`, login validation, `HfApi.whoami` (`hf_api.py:2358-2390`) | Pass through with auth. Offline: 401 `{"error":"Invalid username or password."}` or a synthetic user. HF returns 401 plus `X-Error-Message` when unauthenticated. |
| `GET /api/{type}s/{repo}/revision/{rev}` | covered in §1.2 | |
| `GET /api/{type}s/{repo}/commits/{rev}` | `list_repo_commits` | Pass through. |
| `GET /api/{type}s/{repo}/xet-read-token/{rev}` | hf_xet token refresh (`utils/_xet.py:219-252`) | Pass through when online (see §3.4). Returns headers `X-Xet-Cas-Url`, `X-Xet-Access-Token`, `X-Xet-Token-Expiration` and body `{"casUrl","exp","accessToken"}`. |
| `GET /api/models?search=...` | LM Studio / UI search | Pass through. |
| `GET /api/resolve-cache/{type}s/{repo}/{commit}/{path}?...` | Only reached by following HF's own 307. Unused if you never emit it. | Optional. |
| `GET/HEAD /v2/{ns}/{repo}/manifests/{tag}` and `/v2/{ns}/{repo}/blobs/sha256:{hex}` | Ollama (and llama.cpp builds before Mar 2026) | See §6. |
| `POST /api/{type}s/{repo}/preupload/{rev}`, `/commit/{rev}`, LFS batch `/{repo}.git/info/lfs/objects/batch`, `xet-write-token` | uploads | Out of scope. Return 501/403 or pass through. |
| `GET /api/models/{id}/tree/...?expand=true`, `/api/{type}s/{repo}/lfs-files`, `/discussions` | misc | Pass through. |

Repo-type variants are identical, with `datasets/` or `spaces/` prefixes on resolve and `/api/datasets|spaces/` on the API.

---

## 2. Headers, status codes and exception mapping

### 2.1 Headers huggingface_hub reads

| Header | Where | Semantics |
|---|---|---|
| `X-Repo-Commit` | every resolve response (200/3xx/404) | Full 40-hex commit the revision resolved to. Required on success. On 404, enables `.no_exist` caching. |
| `X-Linked-Etag` | resolve | Preferred etag, i.e. the content id (sha256 for LFS/Xet, git sha1 otherwise). Quoted. |
| `ETag` | resolve | Fallback etag. Weak `W/"..."` tolerated. llama.cpp stores the raw value in `<file>.etag` (legacy path) (`common/download.cpp:79-97,327-353`). |
| `X-Linked-Size` | resolve | True file size (needed because the 3xx `Content-Length` is the size of the redirect body). |
| `Content-Length` | resolve 200 | Size when not redirected. Ignored when `is_redirect`. Needed by llama.cpp's HEAD (it follows redirects and uses the final CL) and by Ollama's blob HEAD. |
| `Location` | resolve 3xx | Relative or absolute. Relative is resolved with `urljoin`. |
| `Content-Range`, `Accept-Ranges` | GET | 206 for ranges. llama.cpp resumes only if `Accept-Ranges != none` (`download.cpp:343-346`) and demands 206 on resume (`download.cpp:228-231`). |
| `Content-Encoding` | GET | Avoid it. If present, huggingface_hub cannot use `Content-Length` (`file_download.py:317-330`). |
| `Content-Disposition` | GET | Used only for the progress-bar filename. |
| `X-Error-Code`, `X-Error-Message` | errors | Exception mapping (below). |
| `Link` | tree, commits, lists: `rel="next"`; resolve: `rel="xet-auth"`, `rel="xet-reconstruction-info"` | Pagination and Xet. |
| `X-Xet-Hash`, `X-Xet-Refresh-Route` | resolve | Xet trigger (§3). |
| `X-HF-Warning`, `RateLimit`, `RateLimit-Policy` | any | Parsed for warnings and 429 messages (`_warn_on_warning_headers`, `parse_ratelimit_headers`). Optional. |

### 2.2 Status to exception mapping (`hf_raise_for_status`, `utils/_http.py:796-990`)

Evaluated in this order:

| Condition | Exception | Effect in `hf_hub_download` |
|---|---|---|
| 3xx | none (not raised) | |
| `X-Error-Code: RevisionNotFound` | `RevisionNotFoundError` | Raised immediately (no cache fallback) (`file_download.py:1839-1841`). |
| `X-Error-Code: EntryNotFound` | `RemoteEntryNotFoundError` (subclass of `EntryNotFoundError`) | Raised immediately. `.no_exist` is written if `X-Repo-Commit` is present. |
| `X-Error-Code: GatedRepo` | `GatedRepoError` (subclass of `RepositoryNotFoundError`) | Falls back to cache, else re-raised. |
| `X-Error-Message == "Access to this resource is disabled."` | `DisabledRepoError` | |
| `X-Error-Code: RepoNotFound`, **or** 401 on `/api/(models|datasets|spaces)/…` or `/…/resolve/…` URLs with a message other than `"Invalid credentials in Authorization header"` | `RepositoryNotFoundError` | Falls back to cache, else re-raised. |
| 400 | `BadRequestError` | |
| 403 | `HfHubHTTPError` ("Forbidden: <X-Error-Message>") | |
| 429 | `HfHubHTTPError` (retry info from `RateLimit` headers) | Retried by `http_backoff` only when `retry_on_errors`. |
| 416 | `HfHubHTTPError` about Range | |
| other 4xx/5xx | `HfHubHTTPError` | 5xx/429/408 retried during downloads (`_DEFAULT_RETRY_ON_STATUS_CODES`). HEAD retries only after the first failure with no local file (`file_download.py:1165-1190`, `_ETAG_RETRY_TIMEOUT=60`). |
| connection error / timeout | stored as `head_call_error` | Cache fallback, else `LocalEntryNotFoundError`. |

The error message is taken from `X-Error-Message` and/or the JSON body `{"error": "..."}` or `{"error": [...]}`
(`_format`, `_http.py:999+`).

**HF's actual error responses (live, 2026-09-23):**

| Case | Status | Headers | Body |
|---|---|---|---|
| Nonexistent repo, unauthenticated | **401** | `X-Error-Message: Invalid username or password.`, `WWW-Authenticate: Bearer realm="Authentication required", charset="UTF-8"`, **no X-Error-Code** | `{"error":"Invalid username or password."}` (API) / plain text (resolve) |
| Nonexistent repo, authenticated | 404 | `X-Error-Code: RepoNotFound` | |
| Missing file | 404 | `X-Error-Code: EntryNotFound`, `X-Error-Message: Entry not found`, `X-Repo-Commit: <sha>` | `Entry not found` |
| Bad revision | 404 | `X-Error-Code: RevisionNotFound`, `X-Error-Message: Invalid rev id: badrev` | `{"error":"Invalid rev id: badrev"}` |
| Gated repo, unauthenticated | **401** | `X-Error-Code: GatedRepo`, `X-Error-Message: Access to model X is restricted. You must have access to it and be authenticated to access it. Please log in.` | same text |
| Gated repo, authenticated but not granted | **403** | `X-Error-Code: GatedRepo` | |
| Gated repo `/api/models/{id}` (metadata) | 200 | `"gated":"manual"` or `"auto"` | Metadata stays public, and `/tree` is public too. |

The proxy must **not** synthesize 401 or `RepoNotFound` for upstream timeouts or 5xx. Return 502/503/504 so clients retry
or fall back (olah PR #62). Also do not treat an upstream 429 or 3xx as "repo missing" (olah PR #77).

### 2.3 Client cache layout the proxy's responses must keep consistent

`~/.cache/huggingface/hub` (`HF_HUB_CACHE`, `HF_HOME/hub`):
```
models--{org}--{name}/            # repo_folder_name: f"{type}s" + "--".join(repo_id.split("/"))  (file_download.py:747-755)
  blobs/{etag}                    # etag = X-Linked-Etag/ETag, normalized. git sha1 (40 hex) or sha256 (64 hex)
  blobs/{etag}.incomplete         # partial download (resume via Range)
  snapshots/{commit}/{path}       # relative symlink -> ../../blobs/{etag}   (commit = X-Repo-Commit)
  refs/{revision}                 # text file containing the commit sha; revision may be nested (refs/pr/1 -> refs/refs/pr/1)
  .no_exist/{commit}/{path}       # negative cache from 404 EntryNotFound + X-Repo-Commit
  trees/{commit}.json             # hub >=1.x tree cache (format_version 1)
.locks/models--{org}--{name}/{etag}.lock
```
Consequences:
* `refs/{rev}` is written from `X-Repo-Commit` (`_cache_commit_hash_for_specific_revision`, `file_download.py:727-743`).
  A wrong commit poisons offline resolution.
* Snapshot folders are keyed by commit. If the proxy invents a commit id for self-hosted content, keep it stable and
  40-hex (`REGEX_COMMIT_HASH=[0-9a-f]{40}`, `file_download.py:80`).
* llama.cpp (Mar 2026+) writes the same layout itself (`common/hf-cache.cpp`, "add standard Hugging Face cache support",
  PR #20775). It names blobs by the tree `lfs.oid` or else `oid` (`hf-cache.cpp:336-362`), validated as 40- or 64-hex.
  **Tree `oid`/`lfs.oid` must therefore equal the ETag you serve**, or llama.cpp and Python end up with duplicate blobs.
* `HF_HUB_OFFLINE=1` (or `TRANSFORMERS_OFFLINE=1`, `constants.py:194`) means the client makes **no requests at all**. The
  proxy never sees them. It only helps if clients already ran online through it once.

---

## 3. Xet storage

### 3.1 What changed

Since 2025 HF stores LFS content in Xet (content-defined chunks in "xorbs" on `cas-server.xethub.hf.co`). Resolve responses
for LFS files now carry Xet headers. Plain-HTTP clients still work because the 302 `Location` points at the
`xet-bridge` CDN (`us.aws.cdn.hf.co/xet-bridge-us/{repo_hash}/{xet_hash}?...signed...`), which serves whole-file bytes
and supports Range.

### 3.2 How huggingface_hub decides to use Xet

`parse_xet_file_data_from_response` (`utils/_xet.py:48-83`):
* It requires the header `X-Xet-Hash`, **and** either `Link` with `rel="xet-auth"` or the header `X-Xet-Refresh-Route`.
  If either is missing, it returns `None`, which means plain HTTP.
* A refresh route starting with `https://huggingface.co/` is rewritten to `HF_ENDPOINT` (lines 77-78). The token refresh
  therefore **comes to the proxy** (`/api/models/{repo}/xet-read-token/{commit}`), but the **CAS traffic goes directly to
  `X-Xet-Cas-Url`** (cas-server.xethub.hf.co). That bypasses a mirror and fails behind firewalls. This is the known
  "set `HF_HUB_DISABLE_XET=1` with hf-mirror" issue.

Download branch (`_download_to_tmp_and_move`, `file_download.py:~2010-2035`):
```
if xet_file_data is not None and is_xet_available(): xet_get(...)
else: http_get(url_to_download, ...)          # warns if xet_file_data and hf_xet not installed
is_xet_available() = not HF_HUB_DISABLE_XET and hf_xet importable   (utils/_runtime.py:155-160)
```
When `xet_file_data` is set, `url_to_download` stays the original resolve URL (the `Location` is not used,
`file_download.py:1817`).

**Tree-cache fast path (huggingface_hub 1.x):** for `revision == <40-hex>` (which is always the case inside
`snapshot_download`), `_xet_file_metadata_from_tree_cache` (`file_download.py:1882-1928`) skips HEAD entirely when
`trees/<commit>.json` has a valid `xet_hash` plus `lfs_sha256` and `lfs_size`. It builds the `XetFileData` with
refresh route `{ENDPOINT}/api/{type}s/{repo}/xet-read-token/{commit}` and goes straight to CAS.

### 3.3 Forcing plain HTTP from the proxy

Apply all of the following:
1. On resolve responses, delete `X-Xet-Hash`, `X-Xet-Refresh-Route`, and the `Link` header, or at least its `xet-auth`
   and `xet-reconstruction-info` entries. The `Link` on resolve carries nothing else.
2. In `/tree`, `/paths-info` and `/api/.../revision` JSON, delete `xetHash` from every entry. `snapshot_download` then
   records `xet_hash=None`, the fast path is not taken, and every file gets a HEAD against the proxy.
3. Rewrite `Location` to the proxy (§1.1). Never forward the signed CDN URL.
4. Stale-client edge case: a client that previously ran against real HF may already have `trees/<commit>.json` with
   `xet_hash` values. It will call the proxy's `xet-read-token` and then contact CAS directly. Pass `xet-read-token`
   through when online (as olah does, `server_api_routes.py:294-342`). Offline, there is no fix apart from
   `HF_HUB_DISABLE_XET=1` or deleting `trees/`. Document `HF_HUB_DISABLE_XET=1` as the belt-and-braces client setting.

This is what olah does: "The 302 deliberately omits `x-xet-hash` so hf_hub does not engage native Xet and instead
downloads over HTTP from olah's content route" (`olah/proxy/files.py:707-767`). It also strips
`XET_RESPONSE_HEADERS = (x-xet-hash, x-xet-refresh-route, x-linked-size, x-linked-etag, link)` when passing responses through.

### 3.4 Fetching upstream bytes for a Xet file (proxy to HF)

The simplest route: `HEAD {upstream}/{repo}/resolve/{commit}/{path}` with `follow_redirects=False` and
`Authorization` → read `Location` (signed xet-bridge URL, around 1 h expiry per the `Expires` param), `X-Linked-Size`,
`X-Linked-Etag` (sha256) and `X-Xet-Hash` → `GET Location` with Range (drop `Authorization`, because the URL is
self-signed). Never persist the signed URL; re-resolve it on each miss (olah `proxy/xet.py:_xet_resolve_url`). A native
Xet client (xet-core/hf_xet) would enable chunk-level dedupe, but it is not needed for an MVP.

### 3.5 Hashes: what is exposed and what you must compute

| Hash | Exposed by HF | Notes |
|---|---|---|
| git blob SHA-1 | `oid` (tree/paths-info), `blobId` (siblings with `blobs=true`), ETag of regular files | For LFS files this is the SHA-1 of the **pointer** text, not the content. |
| SHA-256 of content | `lfs.oid` (tree/paths-info), `lfs.sha256` (siblings), `X-Linked-Etag` on resolve | **Only for LFS/Xet-tracked files.** Regular git files (configs, tokenizer JSON, README) have **no sha256 anywhere in the API**. |
| Xet file hash | `xetHash` (tree/paths-info), `X-Xet-Hash`, CDN `ETag` | **Not** a SHA-256. It is a BLAKE3-keyed Merkle root over content-defined chunks (chunk hash = blake3 keyed with DATA_KEY; internal nodes = blake3(INTERNAL_NODE_KEY, "hash : size\n" lines); file hash = blake3(key=32 zero bytes, root)). The string form byte-swaps each 8-byte word. Spec: https://huggingface.co/docs/xet/en/hashing and `xet-core/xet_core_structures/src/merklehash/aggregated_hashes.rs`. Not worth recomputing. Store it from upstream metadata if needed. |

For a content-addressed store keyed by SHA-256:
* LFS/Xet files: key = `lfs.oid`. Verify while streaming from upstream with `hashlib.sha256`, and reject or evict on
  mismatch.
* Regular files: compute sha256 locally on ingest, and keep a mapping `(repo, commit, path) -> {git_sha1, sha256, size}`,
  because the client-facing ETag must stay the git SHA-1. Verify ingest with
  `sha1(b"blob %d\0" % size + data) == oid`.
* Ollama blob digests are `sha256:<content sha256>`. For GGUF they equal `lfs.oid`, so the same store serves both.

### 3.6 Files larger than 50 GB

`http_get` raises `ValueError("The file is too large to be downloaded using the regular download method…")` when
`expected_size > 50e9` and it is not resuming (`file_download.py:379-385`, `constants.py:41`). Two options:
1. Pass Xet through for those files: keep the Xet headers and `xetHash` for files over 50 GB and let hf_xet talk to HF
   CAS. This is what olah PRs #60, #71 and #72 do, with an opt-in size threshold. It only works online and bypasses the store.
2. Make the size unknown to the client: answer HEAD with `307 Location: http://<alternate-hostname-of-proxy>/blobs/sha256/<hex>?sig=…`
   using a *different hostname* (for example `127.0.0.1` when `HF_ENDPOINT=http://localhost:8080`), and **omit**
   `X-Linked-Size`. The client stops at the cross-host redirect, so `size=None` and the 50 GB guard is skipped. It then
   GETs the alternate URL without `authorization`, which is why the URL carries its own signature. The downside is that
   the client performs no size verification. (Inferred from the code paths; test it before relying on it.)

---

## 4. Auth, gating, offline

* Clients send `authorization: Bearer hf_…` (huggingface_hub `build_hf_headers`, token from `HF_TOKEN` or
  `~/.cache/huggingface/token`, or explicit). llama.cpp sends it from `--hf-token` or `HF_TOKEN` (`common/arg.cpp:3074-3078`),
  on API calls (`hf-cache.cpp:211-213`) and downloads. Ollama sends its own registry token (see §6). text-generation-webui
  uses `HF_TOKEN`.
* The proxy should forward `Authorization` unchanged to upstream for API and resolve calls, and strip it for
  signed-CDN fetches.
* **Access control for cached content:** a gated or private file already in the store must not be served to a caller
  who lacks access. Per request, check authorization upstream (a cheap `HEAD /resolve` or `/api/.../revision` with the
  caller's token, cached per (token hash, repo) for N minutes) before serving from the store. olah keeps a
  `lfs_object_index` plus `authorize_xet_object` for this (`utils/lfs_object_index.py`). For public repos
  (`private:false`, `gated:false`) no per-user check is needed. Offline, be explicit: either serve only repos marked
  public, or trust a local allow-list.
* Gated errors must use `X-Error-Code: GatedRepo` with 401 (anonymous) or 403 (authenticated). A plain 403 becomes a generic
  `HfHubHTTPError`, and a plain 401 on resolve becomes `RepositoryNotFoundError`.
* An invalid token produces a 401 with message `"Invalid credentials in Authorization header"` (stays a generic
  `HfHubHTTPError`, not RepoNotFound).
* Client offline flags: `HF_HUB_OFFLINE=1` / `TRANSFORMERS_OFFLINE=1` (Python, no network at all); llama.cpp
  `--offline` (uses the HF cache only, `arg.cpp:3928-3931`); vLLM passes `local_files_only=HF_HUB_OFFLINE`. The proxy
  cannot influence these. Its own "offline mode" means serving everything it can from the store and returning 504 (not
  401) for anything it lacks.
* `hf auth login` validates tokens through `/api/whoami-v2`. Pass it through, or return a synthetic
  `{"type":"user","name":"local","auth":{"type":"access_token","accessToken":{"role":"read"}},"orgs":[]}` when offline.

---

## 5. Revision semantics

* Accepted forms: branch (`main`), tag (`v1.0`), full sha (40 hex), short sha (≥7 hex, which HF resolves; the client
  regex is `[A-Fa-f0-9]{5,40}`, `constants.py:61`), PR ref `refs/pr/N`, and converter refs `refs/convert/parquet`
  (datasets) / `refs/convert/duckdb`.
* Resolution: mutable refs → commit via upstream (`/api/.../revision/{rev}` → `sha`, or `/refs`). Cache mutable→commit
  mappings with a short TTL (for example 60 s to 10 min). Cache `commit`-addressed metadata and trees **forever**, because
  commits are immutable (huggingface_hub relies on the same fact, see `_tree_cache.py` docstring).
* Pinning: a client passing a 40-hex revision gets fully cacheable answers. If huggingface_hub already has
  `snapshots/<sha>/<file>`, it makes **no request at all** (`file_download.py:1098-1113`). The proxy can offer a
  "pin" configuration that maps `{repo, main}` → fixed commit for reproducible fleets. If you do, `X-Repo-Commit` must
  report the pinned sha.
* Offline behaviour for branch names: answer from the last known ref mapping, and still set `X-Repo-Commit`.
* Ref names in client caches are written to `refs/<rev>` literally, so `refs/pr/1` becomes the nested file
  `refs/refs/pr/1`. Not the proxy's problem, just FYI.

---

## 6. llama.cpp and Ollama specifics

### 6.1 llama.cpp (`-hf user/repo[:quant]`, `-hff file`, `--hf-token`)

Endpoint selection (`common/common.cpp:1554-1567`):
```cpp
endpoint = getenv("MODEL_ENDPOINT"); if empty -> getenv("HF_ENDPOINT"); if empty -> "https://huggingface.co/";
ensure trailing '/'
```
So `MODEL_ENDPOINT=http://localhost:8080/` works, and plain `http://` needs no TLS build.

**Current flow (since 2026-03-24, `common/hf-cache.cpp`, `common/download.cpp`):**
1. `GET {EP}api/models/{repo}/refs` with `User-Agent: llama-cpp/<build>`, `Accept: application/json` and optional Bearer.
   It reads `branches[].{name,targetCommit}` and prefers `main`.
2. `GET {EP}api/models/{repo}/tree/{commit}?recursive=true`. It reads `type`, `path`, `lfs.oid` or `oid`.
   **It does not paginate.**
3. It selects the GGUF (`find_best_model`, `download.cpp:660-720`): with tag T, regex `T[.-]` case-insensitive over
   `*.gguf` names, excluding `mmproj`, `imatrix`, `mtp-`, `eagle3-`, `dflash-` and `dspark-` files; the default tags are
   `Q4_K_M` then `Q8_0`, then the first GGUF. For split files it takes `-00001-of-N` and then all parts. It auto-selects
   `mmproj*` siblings. A `preset.ini` in the repo short-circuits selection.
4. Downloads `{EP}{repo}/resolve/{commit}/{path}` with cpp-httplib `set_follow_location(true)` (`common/http.h:124`):
   `HEAD` (follows redirects; reads final `ETag`, `Content-Length`, `Accept-Ranges`), then `GET`, with
   `Range: bytes=N-` for resume (expects 206). HF files run with `skip_etag` (the file is keyed by oid in the blobs dir).
5. Writes into the standard HF cache (`LLAMA_CACHE`, `HF_HUB_CACHE`, `HF_HOME/hub`, `~/.cache/huggingface/hub`).

Implications: `/refs` and unpaginated `/tree` are mandatory. `resolve` must answer HEAD with a 2xx at the end of the redirect
chain, with `Content-Length`. Redirects to another host are followed (cpp-httplib), so the CDN would be hit if you leaked
its Location.

**Legacy flow (builds before March 2026, for example `9e118b97` `common/download.cpp:565-643`):**
`GET {EP}v2/{repo}/manifests/{tag}` (tag defaults to `latest`), `Accept: application/json`,
**`User-Agent: llama-cpp/...` required** to receive `ggufFile` and `mmprojFile`. It then downloads
`{EP}{repo}/resolve/main/{ggufFile.rfilename}`, and the ETag is stored in `<file>.etag` to decide re-downloads. The
manifest is cached at `get_manifest_path`. Live response for UA `llama-cpp`:
```json
{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",
 "config":{"digest":"sha256:4549…","mediaType":"application/vnd.docker.container.image.v1+json","size":551},
 "layers":[{"digest":"sha256:6f85…","mediaType":"application/vnd.ollama.image.model","size":807694464},
           {"digest":"sha256:948a…","mediaType":"application/vnd.ollama.image.template","size":1481},
           {"digest":"sha256:6c0b…","mediaType":"application/vnd.ollama.image.params","size":65}],
 "ggufFile":{"rfilename":"Llama-3.2-1B-Instruct-Q4_K_M.gguf","blobId":"sha256:6f85…","size":807694464,
             "lfs":{"sha256":"6f85…","size":807694464,"pointerSize":134}},
 "mmprojFile":{...same shape..., present for vision repos; plus an "application/vnd.ollama.image.projector" layer}}
```
HF responds with `Vary: Origin,User-Agent`, `cache-control: public, max-age=60`, and a weak `ETag`. Key your proxy cache on
"UA starts with llama-cpp" versus the rest.

### 6.2 Ollama (`ollama pull hf.co/{user}/{repo}[:{quant}]`)

Name parsing (`types/model/name.go:140-200`): `[scheme://]host/namespace/model[:tag]`, so `hf.co/bartowski/X:Q4_K_M` has
host `hf.co`, namespace `bartowski`, model `X`, tag `Q4_K_M` (default tag `latest`). Base URL is `{scheme}://{host}`
(`name.go:317-322`).

**No `HF_ENDPOINT` support.** Ollama always talks to the host inside the model name. Ways to use a proxy:
1. **Use the proxy host in the name:** `ollama pull localhost:8080/bartowski/X:Q4_K_M --insecure` (or
   `http://localhost:8080/...`). `--insecure` is required for http (`images.go:939-940,1392-1394`) and also relaxes the
   cross-host redirect policy. The downside is that models are stored under the name `localhost:8080/...` instead of `hf.co/...`.
2. **Keep the `hf.co/...` names:** redirect DNS for `hf.co` to the proxy (hosts file) and give it a TLS cert trusted by
   the system root store (Go uses system roots). Alternatively set `HTTPS_PROXY` to a MITM proxy with a trusted CA
   (Ollama honours `HTTPS_PROXY`, `envconfig/config.go:345`).

Registry protocol as Ollama uses it (`server/images.go`, `server/download.go`):
1. `GET /v2/{ns}/{repo}/manifests/{tag}` with `Accept: application/vnd.docker.distribution.manifest.v2+json` and
   `User-Agent: ollama/<ver> (<arch> <os>) Go/<ver>` (`images.go:1279-1301,1411`). The response is the manifest above
   **without** `ggufFile` (verified with an Ollama UA). A 404 becomes `os.ErrNotExist`. A 401 triggers the challenge flow:
   parse `WWW-Authenticate` (Bearer realm/service/scope), call `getAuthorizationToken` with an Ollama-key-signed request, and retry
   (`images.go:1316-1370`, `auth.go:53-73`). HF answers gated or private repos with
   `401 WWW-Authenticate: Basic realm="https://huggingface.co/api/models/{repo}/ssh-auth"` plus `X-Error-Code: GatedRepo`,
   because HF authenticates Ollama through the user's registered Ollama SSH public key. Treat that as pass-through only.
   An invalid quant tag gives `400 {"error":"The specified tag is not a valid quantization scheme. Please use another tag or \"latest\""}`.
2. For each layer (and the config): `HEAD /v2/{ns}/{repo}/blobs/sha256:{hex}` follows redirects and reads
   `Content-Length` (`download.go:150-156`). Parts are 16 × clamp(total/16, 100 MB, 1000 MB).
3. **Direct URL discovery** (`download.go:231-273`): `GET` the blob URL with a redirect policy that follows only same-hostname
   redirects and stops at the first cross-host one. **The status must be 307 or 200, and `resp.Location()` must succeed**.
   For a 200, that means **a `Location` header must be present**: HF returns `200` with `location: ?__sign=<jwt>` for small
   blobs (config, template, params), and `307 Location: /{repo}/resolve/main/{file}.gguf` then `307` to the CDN for the
   model layer. The model blob chain is HF's `/v2/.../blobs/sha256:X`, then 307 to `/resolve/main/<gguf>` (same host,
   followed), then 307 to the CDN (different host, stops there), so `directURL` is the CDN URL.
4. Parallel `GET directURL` with `Range: bytes=a-b` using `http.DefaultClient`, with **no auth header** (`download.go:336-350`).
   Then it verifies `sha256(file) == digest` (`images.go:1082,1494-1508`).
5. Cross-host redirects outside `ollama.com|ollama.ai|hf.co|huggingface.co` are blocked unless `--insecure`
   (`images.go:1420-1447`).

Proxy design for Ollama:
* `GET|HEAD /v2/{ns}/{repo}/manifests/{tag}`: proxy upstream and cache per (repo, tag, UA class), keyed to the repo's
  current commit. Offline, serve the cached body.
* `HEAD /v2/{ns}/{repo}/blobs/sha256:{hex}` → `200` with `Content-Length`, `Docker-Content-Digest: sha256:{hex}` and
  `Accept-Ranges: bytes`.
* `GET /v2/{ns}/{repo}/blobs/sha256:{hex}` → `200` **with `Location: /blobs/sha256/{hex}?sig=…`** (same host, self-signed,
  because later range requests carry no auth), or `307` to that URL. Serve the bytes from the store with Range.
* Template, params and config blobs are **generated by HF** (the Go chat template, stop params, and a config JSON whose
  `rootfs.diff_ids` lists the layer digests). There is no repo file behind them. Cache them from upstream as opaque
  blobs keyed by digest. Synthesising them offline is possible but out of MVP scope.
* The model layer digest equals the GGUF `lfs.oid`, so dedupe with the huggingface_hub/llama.cpp store is free.

### 6.3 Other clients

* **text-generation-webui** `download-model.py`: `base = HF_ENDPOINT or https://huggingface.co` (line 29). It calls
  `GET {base}/api/models/{model}/tree/{branch}` (non-recursive) with its own cursor loop (§1.3). It downloads
  `{base}/{model}/resolve/{branch}/{file}` with `Range: bytes=N-` resume, sends `HF_TOKEN` as Bearer, and checks
  sha256 of LFS files against `lfs.oid`.
* **transformers / diffusers / vLLM / `hf download`**: all go through huggingface_hub (`hf_hub_download`, `snapshot_download`,
  `model_info`, `list_repo_tree`, `HfFileSystem`, `get_safetensors_metadata`). `get_safetensors_metadata` does
  `GET resolve/.../model.safetensors` with `range: bytes=0-100000`, then `bytes=8-{n+7}` (`hf_api.py:7088-7100`), so Range
  on GET with redirects followed must work. vLLM sets `HF_XET_HIGH_PERFORMANCE` (`weight_utils.py:76`), which is irrelevant
  once Xet is stripped. `HF_HUB_ENABLE_HF_TRANSFER` is deprecated in 1.x (`constants.py:299-305`).
* **LM Studio**: no documented `HF_ENDPOINT` support (open request lmstudio-ai/lms#104). Third-party posts claim an
  `hf_endpoint` config key, but it is unverified. Plan on DNS/TLS interception or importing GGUFs into LM Studio's models
  directory.

---

## 7. Lessons from olah and other mirrors

**olah (vtuber-plan/olah)** is a FastAPI + httpx mirror (`server_file_routes.py`, `server_api_routes.py`).
* **Architecture:** meta and API responses are cached per repo/commit on disk. LFS/Xet files are cached as a **block cache**
  (`cache/olah_cache.py`, format v10, with a bitmap presence map, per-block single-flight fetch, and lock-aware LRU
  eviction), so range requests fill blocks lazily. Xet files get a stable URL
  `/xet-bridge-{region}/{repo_hash}/{xet_hash}`, and the signed upstream URL is re-resolved on each miss (`proxy/xet.py`).
  An object index (`utils/lfs_object_index.py`) maps oid/xet hash → (repo, path, commit) for re-resolution and
  authorization. Allow and deny rules exist per repo for proxying and caching. A "mirrors-path" option serves local git
  repos (issue #37/#39/#41: the ETag there must be the git sha1 for non-LFS files).
* **Bugs and pitfalls from its issues and PRs:**
  * #85 (open, 2026-09): llama.cpp `-hf` fails because `/api/models/{repo}/refs` is not implemented.
  * #52: HF's relative 307 to `/api/resolve-cache/...` was not followed. Use a client that follows relative redirects.
  * #57: case-insensitive and legacy repo names need to follow HF's 307 canonicalisation.
  * #58: a zero-length file produced `Range: bytes=0--1`, a 500 on HEAD, and broke `snapshot_download` for repos with empty
    `__init__.py`. Special-case size 0: no `Content-Range`, and `Content-Length: 0`.
  * #62 / #77: upstream timeouts, 5xx, 429 and unfollowed redirects were turned into 401 RepoNotFound, which is fatal and
    non-retryable in clients. Use 504, and follow redirects before deciding existence.
  * #69 / #61: pass upstream 4xx through verbatim, including `X-Error-Code`, instead of generic errors.
  * #32: offline mode returned 401 even for cached repos. The offline path must answer `/api/.../revision/...` from the cache,
    or `snapshot_download` falls back to "local_dir exists".
  * #36 / #64 / #66 / #79: range or chunked downloads missed the cache or corrupted blocks under concurrency. Needed
    single-flight per block, atomic writes, and streaming a block while it is being fetched.
  * #43: an upstream connectivity check every 5 s was excessive. Check lazily or with backoff.
  * #50 / #60 / #71: files over 50 GB fail over plain HTTP in huggingface_hub (see §3.6), fixed with opt-in Xet pass-through.
  * #53 / #51: slow cache hits of about 100 MB/s from Python streaming. Use `sendfile` or zero-copy for fully cached blobs.
  * #47 / #49: brotli-compressed upstream responses. Request `Accept-Encoding: identity` upstream, or decode.
* **hf-mirror.com:** a public reverse proxy for mainland China. From outside China it now answers with
  `308 → https://huggingface.co/...` (verified 2026-09-23). Documented practice is `HF_ENDPOINT=https://hf-mirror.com`
  **plus `HF_HUB_DISABLE_XET=1`**, because with hf_xet installed the client follows the Xet path to
  `cas-server.xethub.hf.co`, ignores `HF_ENDPOINT` and gets 401s (see the jundot/omlx#142 and
  NousResearch/hermes-agent#111072 discussions). That is the failure mode our header and JSON stripping avoids.
* **General:** keep `Content-Length` exact and never re-compress. Emit `access-control-expose-headers` (as HF does) if
  browser clients matter. Rewrite every absolute `https://huggingface.co` URL in `Location`, `Link` and JSON bodies to the
  proxy's external base URL, derived from the `Host` / `X-Forwarded-*` headers.

---

## 8. MVP endpoint set (prioritised)

**P0: huggingface_hub `hf_hub_download` / `snapshot_download` / transformers / diffusers / vLLM work end-to-end**

| # | Route | Notes |
|---|---|---|
| 1 | `HEAD, GET /{[datasets/|spaces/]}{repo_id}/resolve/{rev}/{path}` | §1.1 Model A. Headers: `X-Repo-Commit`, `ETag` + `X-Linked-Etag` (git sha1 or sha256), `Content-Length`, `Accept-Ranges`, Range/206/416, 404 `EntryNotFound` + `X-Repo-Commit`, 404 `RevisionNotFound`. No Xet headers. Stream-through on miss: start sending to the client while writing to the store and hashing. |
| 2 | `GET /api/{models|datasets|spaces}/{repo_id}` and `/revision/{rev}` (`?blobs=true`, `expand`) | Must include `id`, `sha`, `siblings[].rfilename`. Strip `xetHash`. Canonicalise legacy ids with a relative 307. |
| 3 | `GET /api/{type}s/{repo_id}/tree/{rev}[/{path}]?recursive=&expand=` | Strip `xetHash`. Return all entries unless `limit` is given. Rewrite `Link`. Support the `cursor` decode for text-generation-webui. |
| 4 | Error semantics | §2.2 table. Upstream down → 504, not 401. |

**P1: llama.cpp, Ollama, extra huggingface_hub features**

| # | Route | Notes |
|---|---|---|
| 5 | `GET /api/{type}s/{repo_id}/refs[?include_prs=1]` | Required by llama.cpp ≥ Mar 2026. |
| 6 | `POST /api/{type}s/{repo_id}/paths-info/{rev}` | Form or JSON `paths[]`. Strip `xetHash`. Used by `HfFileSystem` (vLLM). |
| 7 | `GET /v2/{ns}/{repo}/manifests/{tag}` | Cache by UA class (llama-cpp vs other). Pass-through plus offline replay. |
| 8 | `HEAD, GET /v2/{ns}/{repo}/blobs/sha256:{hex}` | 200 + `Location` self-URL (signed), or 307. Range support. Content-Length on HEAD. |
| 9 | `GET /blobs/sha256/{hex}` (internal content-addressed route, signed query) | Target for redirects and for Ollama's un-authenticated range GETs. |
| 10 | `GET /api/whoami-v2` | Pass-through or synthetic. |

**P2: nice to have**
* `GET /api/{type}s/{repo}/xet-read-token/{rev}`: pass-through, for stale tree caches and >50 GB Xet passthrough.
* `GET /api/{type}s/{repo}/commits/{rev}`, `/api/models?search=` (pass-through).
* `refs/pr/N` and `refs/convert/*` revisions, short-sha revisions.
* Revision pinning configuration, access-control cache per token, and an admin "prefetch repo@rev" endpoint.
* Uploads: `preupload`, `commit`, LFS batch. Reject with 403/501 or pass through.

**Store and metadata model the routes above need** (per `(repo_type, repo_id)`):
```
refs:    rev -> commit (TTL for mutable refs; permanent for 40-hex)
commit:  commit -> { repo_info_json (raw upstream, pre-strip), tree: [ {path, type, size, oid(git sha1), lfs_sha256?, lfs_size?, xet_hash?} ] }
blobs:   sha256 -> file (verified); alias git_sha1 -> sha256 for regular files
ollama:  (repo, tag, ua_class) -> manifest body + commit; digest -> blob (config/template/params stored as opaque blobs)
```

**Verification checklist for the implementation:**
1. `HF_ENDPOINT=http://localhost:8080 python -c "from huggingface_hub import snapshot_download; snapshot_download('openai-community/gpt2', allow_patterns=['*.json','*.safetensors'])"`
   with hf_xet **installed** and `HF_HUB_DISABLE_XET` unset. The proxy logs must show no calls to `xet-read-token`, and
   there must be no outbound CAS traffic.
2. Run it again with the upstream disconnected; it must succeed from the proxy store.
3. `hf_hub_download(..., revision="refs/pr/1")`, a nonexistent file (expect `.no_exist` to be written), a bad revision
   (expect `RevisionNotFoundError`), and a gated repo without a token (expect `GatedRepoError`).
4. `MODEL_ENDPOINT=http://localhost:8080/ llama-cli -hf bartowski/Llama-3.2-1B-Instruct-GGUF:Q4_K_M`.
5. `ollama pull localhost:8080/bartowski/Llama-3.2-1B-Instruct-GGUF:Q4_K_M --insecure`.
6. `HF_ENDPOINT=http://localhost:8080 python download-model.py <repo>` (text-generation-webui). It must terminate.
7. An empty file in the repo (0 bytes), a repo with more than 1000 files (pagination), and resume after killing a download
   mid-way (206).

---

## Sources

* huggingface_hub source: https://github.com/huggingface/huggingface_hub/tree/main/src/huggingface_hub (`file_download.py`,
  `constants.py`, `hf_api.py`, `_snapshot_download.py`, `_tree_cache.py`, `utils/_http.py`, `utils/_xet.py`,
  `utils/_pagination.py`, `utils/_runtime.py`); v0.36.0 `_snapshot_download.py`
* llama.cpp: https://github.com/ggml-org/llama.cpp/blob/master/common/hf-cache.cpp, `common/download.cpp`,
  `common/common.cpp` (`common_get_model_endpoint`), `common/http.h`, `common/arg.cpp`; legacy manifest flow at commit
  `9e118b97c456d378dea49a8c46b850c71fc18363` `common/download.cpp`
* Ollama: https://github.com/ollama/ollama/blob/main/server/images.go, `server/download.go`, `server/auth.go`,
  `types/model/name.go`, `envconfig/config.go`
* olah: https://github.com/vtuber-plan/olah (`src/olah/proxy/files.py`, `proxy/xet.py`, `server_api_routes.py`,
  `server_file_routes.py`) and issues #32, #36, #43, #50, #52, #53, #57, #58, #62, #69, #71, #72, #77, #85
* text-generation-webui: https://github.com/oobabooga/text-generation-webui/blob/main/download-model.py
* vLLM `vllm/transformers_utils/repo_utils.py`, `model_executor/model_loader/weight_utils.py`; diffusers
  `pipelines/pipeline_utils.py`; transformers `utils/hub.py`
* Xet hashing spec: https://huggingface.co/docs/xet/en/hashing
* HF_ENDPOINT + HF_HUB_DISABLE_XET mirror issues: https://github.com/jundot/omlx/issues/142,
  https://github.com/NousResearch/hermes-agent/issues/111072, https://github.com/huggingface/huggingface_hub/issues/3266
* HF env vars: https://huggingface.co/docs/huggingface_hub/en/package_reference/environment_variables
* LM Studio endpoint request: https://github.com/lmstudio-ai/lms/issues/104
* Live probes of huggingface.co, hf.co and hf-mirror.com on 2026-09-23 (headers quoted inline above)
