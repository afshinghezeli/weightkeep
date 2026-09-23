# Roadmap

Tasks are small enough for one PR each. A task is done when its acceptance criteria are met, `make check`
passes, and the box is ticked in the same PR. "after X" means X must be done first.

Design background: [`design.md`](design.md). Decisions: [`adr/`](adr/).

## M0 Foundation

- [x] **M0.1 Project conventions.** Licence, code of conduct, contributing guide, security policy,
  commit and versioning rules, writing style, design doc, ADRs 0001-0008.
- [x] **M0.2 Claude Code setup.** `CLAUDE.md`, project skills, prose and gofmt hooks.
- [x] **M0.3 Go skeleton.** after M0.1
  `go.mod`, `cmd/weightkeep`, `internal/cli` with a `version` command, `internal/version` filled by
  ldflags, `Makefile` (build, test, lint, check), `.golangci.yml`.
  Accept: `make check` passes; `./bin/weightkeep version` prints version, commit and Go version.
- [x] **M0.4 CI and repo templates.** after M0.3
  `ci.yml` (lint; test on ubuntu, macos, windows; `go mod tidy` diff check), PR title lint,
  Dependabot for gomod and actions, issue forms, PR template. Actions pinned by SHA,
  `permissions: contents: read` by default.
  Accept: CI green on `main`.
- [x] **M0.5 Release plumbing.** after M0.4
  release-please config and workflow, `.goreleaser.yaml` (archives, checksums, deb/rpm/apk packages,
  build provenance attestations). Homebrew and Scoop need their own repos and move to M5.2.
  Accept: `goreleaser check` passes; `goreleaser release --snapshot --clean` builds all targets locally.

## M1 Keep

- [x] **M1.1 Config and paths.** after M0.3
  `WEIGHTKEEP_HOME` or `$XDG_DATA_HOME/weightkeep` or `~/.local/share/weightkeep` (Windows:
  `%LOCALAPPDATA%\weightkeep`); optional `config.toml`; Hub endpoint (`WEIGHTKEEP_UPSTREAM`, default
  `https://huggingface.co`); token discovery in huggingface_hub's order (`HF_TOKEN`, `HF_TOKEN_PATH`,
  `$HF_HOME/token`, `~/.cache/huggingface/token`).
  Accept: table tests for every precedence rule; tokens never appear in `String()` or logs.
  Done: also added `weightkeep env` (like `go env`) to show the resolved settings in bug reports.
- [x] **M1.2 Blob store.** after M1.1
  `Put(io.Reader) (Blob, error)` streams to `tmp/`, computes SHA-256 and git blob SHA-1 in one pass,
  fsyncs, renames to `blobs/sha256/ab/<hex>`, sets 0444. `Open`, `Has`, `Verify`. SQLite (modernc, WAL)
  with a `blobs` table and a migration runner.
  Accept: concurrent `Put` of identical content leaves one blob and no temp files; a corrupted blob
  fails `Verify`; killing mid-`Put` leaves nothing under `blobs/`.
- [x] **M1.3 Hub client.** after M1.1
  Resolve a revision to a commit; list the tree recursively (following `Link` pagination); file
  metadata via HEAD without following cross-host redirects; ranged GET that follows redirects and sends
  `Authorization` only to the Hub host. Typed errors: repo not found, revision not found, entry not
  found, gated, unavailable.
  Accept: unit tests against an `httptest` fake Hub covering each error; one network test against
  `prajjwal1/bert-tiny`.
- [x] **M1.4 Manifest.** after M1.2
  `Manifest{Repo, Type, Commit, FetchedAt, Files[{Path, Size, SHA256, GitSHA1, LFS}], License}` with
  canonical JSON (sorted, no insignificant whitespace), stored under `manifests/` and indexed in SQLite
  (`revisions`, `files`, `refs`).
  Accept: round-trip test; canonical form is byte-stable across runs.
- [x] **M1.5 Fetcher.** after M1.2, M1.3
  Download one file into the store: resume from `tmp/` with Range, verify against the expected SHA-256
  (LFS) or git SHA-1 (regular file), retry with backoff on 5xx/timeouts, never on 4xx. Parallel across
  files with a limit. Progress events on a channel.
  Accept: tests for resume after a dropped connection, hash mismatch (blob rejected), 404, and 503
  retried then succeeding.
- [x] **M1.6 `pull`.** after M1.4, M1.5
  `weightkeep pull org/model[@rev] [--include GLOB]... [--exclude GLOB]...`. Records the manifest and the
  ref. Progress on stderr. Re-running is a no-op that exits 0.
  Accept: pulling `prajjwal1/bert-tiny` twice; the second run makes only the revision request.
  Done: filters only choose LFS files; small files are always kept and the manifest lists the whole
  tree. A pinned commit that is already kept pulls with no network. Orchestration is in `internal/keep`.
- [ ] **M1.7 `ls` and `verify`.** after M1.6
  `ls` shows repo, commit (short), size, file count, last verified; `--json`. `verify` re-hashes all or
  one revision and reports mismatches with a non-zero exit.
  Accept: flipping a byte in a blob (after chmod) makes `verify` fail and name the file.
- [ ] **M1.8 `export`.** after M1.6
  Materialise a revision into the HF cache layout (`models--org--name/{blobs,snapshots,refs}`) at
  `$HF_HUB_CACHE` or `--cache-dir`, trying reflink, hardlink, symlink, copy in that order;
  `--to DIR` writes a plain directory instead.
  Accept: after export, `HF_HUB_OFFLINE=1` transformers loads `prajjwal1/bert-tiny` (compat test).
- [ ] **M1.9 `gc`.** after M1.6
  Remove blobs no manifest references, stale `tmp/*.lock` files, and partials older than a week;
  `--dry-run`.
  Accept: test with two revisions sharing blobs; removing one revision's manifest keeps shared blobs.

## M2 Serve (0.1.0)

- [ ] **M2.1 Proxy routing.** after M1.4
  `weightkeep serve [--addr 127.0.0.1:8700]`. Parse resolve and API paths: legacy single-segment ids,
  `datasets/` and `spaces/` prefixes, URL-encoded revisions (`refs%2Fpr%2F1`), paths with slashes.
  Request logging. External base URL from `Host` and `X-Forwarded-*`.
  Accept: table tests for path parsing, including hostile paths (`..`, encoded slashes in repo ids).
- [ ] **M2.2 Resolve from the store.** after M2.1
  HEAD/GET `/{repo}/resolve/{rev}/{path}` with every header in ADR 0004, Range (206, 416), zero-length
  files, `EntryNotFound` and `RevisionNotFound` 404s with `X-Repo-Commit`.
  Accept: handler tests for each header and status.
- [ ] **M2.3 Repo info, tree and refs.** after M2.2
  `/api/models/{repo}[/revision/{rev}]`, `/tree/{rev}[/{path}]` (unpaginated unless `limit`; cursor
  support), `/refs`. Online: upstream JSON with `xetHash` removed and URLs rewritten. Offline:
  synthesised from the manifest.
  Accept: offline `snapshot_download` of a pulled repo succeeds through the proxy.
- [ ] **M2.4 Pull-through.** after M2.3, M1.5
  On a miss, resolve and fetch from upstream, streaming to the client while writing to the store.
  One upstream fetch per blob no matter how many clients ask (single flight).
  Accept: two concurrent clients, one upstream request; client receives verified bytes.
- [ ] **M2.5 Passthrough and paths-info.** after M2.3
  `POST /paths-info/{rev}`, `/api/whoami-v2`, and a generic passthrough for other `/api/` routes, all
  with Xet stripping and URL rewriting.
- [ ] **M2.6 Error semantics and offline mode.** after M2.4
  Upstream timeout/5xx → 504; upstream 4xx passed through verbatim; `--offline` never contacts upstream.
  Accept: tests for each row of the error table in the hf-protocol skill.
- [ ] **M2.7 Client compatibility suite.** after M2.6
  `test/compat/` run by `make compat` and a CI job: huggingface_hub `snapshot_download` and
  `hf_hub_download` with `hf_xet` installed (no Xet traffic allowed), transformers offline load,
  text-generation-webui style cursor pagination, llama.cpp `-hf` when the binary is available.
  Accept: suite green locally and in CI.
- [ ] **M2.8 Ollama routes.** after M2.7
  `/v2/{ns}/{repo}/manifests/{tag}` and `/v2/{ns}/{repo}/blobs/sha256:{hex}` per the hf-protocol notes.
  Accept: `ollama pull 127.0.0.1:8700/bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M --insecure` works.
- [ ] **M2.9 README and 0.1.0.** after M2.7, M0.5
  README with install, quick start, how it works, comparison, limits, FAQ (licences first). Opening and
  "why" left for the maintainer to write. Release 0.1.0.

## M3 Share (0.2.0)

- [ ] **M3.0 Spike: web seeds against the Hub.** after M1.3
  anacrolix web seed peer fetching a commit-pinned repo for over an hour (signed URL expiry, relative
  redirects, token only to huggingface.co, throughput). Result recorded in ADR 0005.
- [ ] **M3.1 Licence detection.** after M1.6
  At pull time record `cardData.license`, `license_name`, `license_link`, `gated`, LICENSE file path
  and SHA-256, `base_model`. Compute the tier from `internal/policy` data.
  Accept: table test per licence id in the licence-policy reference; gated always C.
- [ ] **M3.2 Hybrid torrent builder.** after M3.0
  One pass computes v1 SHA-1 pieces (with BEP 47 padding), v2 merkle roots and piece layers, and plain
  SHA-256. `info.name` = commit, `url-list` = Hub resolve base.
  Accept: infohashes match libtorrent's for the same files (compat test with python-libtorrent).
- [ ] **M3.3 Torrent client over the store.** after M3.2
  anacrolix client with storage mapped onto `blobs/` and `tmp/`; piece completion in our SQLite.
- [ ] **M3.4 `seed`.** after M3.1, M3.3
  Seed tier A by default, B1/B2 only with per-model opt-in; disk budget, upload rate limit, monthly cap.
  Accept: a tier C revision is refused with a message naming the rule.
- [ ] **M3.5 Multi-source fetch.** after M3.3
  Fetch scheduler across Hub, swarm and configured HTTP/IPFS-gateway mirrors, piece-aligned ranges
  verified against v2 piece layers.
  Accept: with upstream blocked, a second node pulls a revision from the first node's seed.

## M4 Trust

- [ ] **M4.1 OMS export and verify.** after M1.4
  `weightkeep manifest export --oms` and `weightkeep manifest verify`; ECDSA P-256 keys; OMS conformance
  suite in CI.
- [ ] **M4.2 Registry repo and submission CI.** after M4.1, M3.1
  Separate `weightkeep-registry` repo: layout, submission checks (re-fetch Hub metadata, tier, gating,
  denylist), tuf-on-ci signing.
- [ ] **M4.3 `registry sync`.** after M4.2
  go-tuf client with the root pinned in the binary.
- [ ] **M4.4 Denylist.** after M4.3
  Refuse to seed or serve-to-others anything on the signed denylist.
- [ ] **M4.5 Namespace checks.** after M4.3
  On pull, compare against registry manifests; warn loudly when an `org/name` now serves different
  content for a commit or when a repo was deleted and re-created.

## M5 Launch

- [ ] **M5.1 Demo.** vhs tape committed, GIF rendered in CI.
- [ ] **M5.2 Install paths.** `install.sh`; Homebrew tap and Scoop bucket repos wired into GoReleaser;
  all tested on clean machines.
- [ ] **M5.3 Docs pass.** Usage docs for each client (transformers, vLLM, llama.cpp, Ollama, LM Studio).
