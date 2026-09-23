# weightkeep

Go CLI that keeps verified copies of open-weight model revisions and serves them through a local
Hugging Face Hub-compatible endpoint, with BitTorrent/mirror fallback. Module
`github.com/afshinghezeli/weightkeep`, Apache-2.0, public repo.

Read before non-trivial work: `docs/design.md` (architecture), `docs/roadmap.md` (tasks),
`docs/adr/` (decisions). Research notes behind the decisions are in `.notes/research/` (local only,
gitignored).

## Commands

- Build: `make build` (outputs `./bin/weightkeep`)
- Unit tests: `make test`; one package: `go test ./internal/store/...`
- Lint: `make lint` (golangci-lint v2, config in `.golangci.yml`)
- Everything CI checks: `make check`. Run it before saying a Go change is done.
- Client compatibility tests: `make compat` (needs uv and network)
- Network tests are opt-in: `WEIGHTKEEP_NETWORK_TESTS=1 go test ./...`

## Layout

- `cmd/weightkeep/`: main, wiring only
- `internal/cli`: cobra commands; no business logic
- `internal/{config,hub,store,manifest,fetch,hfcache,proxy,policy,torrent,registry}`: see the package
  table in `docs/design.md`. Imports go down that table, never up.
- `test/compat/`: Python scripts that run real clients against `weightkeep serve`
- `docs/adr/`: MADR records; `docs/contributing/`: commit rules and writing style

## Workflow

- Work through `docs/roadmap.md` one task at a time with the `task` skill. Finish, verify, commit,
  tick the box, report, then stop unless told to continue.
- Commit with the `commit` skill. Conventional Commits, scopes listed in
  `docs/contributing/commits.md`. Stage files by name. No `Co-Authored-By` or "Generated with" lines.
- Changing the store layout, SQLite schema, manifest format, proxy wire behaviour, licence policy or a
  core dependency needs an ADR first (`adr` skill).
- Never push, tag, or create GitHub releases unless the user asks in this session.

## Go rules

- Go 1.25+ (go.mod), developed on 1.27. `CGO_ENABLED=0` must always build.
- stdlib `net/http` for the proxy and Hub client; no web framework.
- SQLite via `modernc.org/sqlite` only (pure Go). BitTorrent via `github.com/anacrolix/torrent` only
  inside `internal/torrent`.
- Wrap errors with context: `fmt.Errorf("open blob %s: %w", sum, err)`. Return or log, not both.
- No `panic` outside `main` and tests. No global mutable state; pass dependencies in.
- `context.Context` first argument on anything that does I/O.
- Structured logging with `log/slog`. Never log tokens or `Authorization` headers.
- Tests: table-driven, `t.TempDir()`, `httptest` fake Hub. No network unless guarded by
  `testutil.Network(t)`.
- Only `internal/store` writes under `blobs/`. Blobs are written to `tmp/`, hashed, fsynced, renamed.
- Hashes are lowercase hex strings. SHA-256 is the identity of a blob; git SHA-1 is kept for Hub ETags.

## Proxy rules (details: `hf-protocol` skill)

- `X-Repo-Commit` on every resolve response, including 404.
- ETag = git SHA-1 for regular files, SHA-256 for LFS files; must match tree `oid`/`lfs.oid`.
- Strip all Xet headers and every `xetHash` JSON field. Never forward CDN `Location`s.
- Upstream failure is 502/504, never 401 or `RepoNotFound`.

## Models in tests, docs and examples

Tier A licences only (`licence-policy` skill). Defaults: `prajjwal1/bert-tiny` (MIT) for tests,
`HuggingFaceTB/SmolLM2-135M` (Apache-2.0) for examples, `bartowski/SmolLM2-135M-Instruct-GGUF` for
llama.cpp/Ollama. Never put a gated model name in a test or example.

## Writing

Docs, help text and errors follow `docs/contributing/style.md` (`docs-voice` skill). A hook flags
filler words in Markdown. The README opening, "why" section and all launch posts are written by the
maintainer; leave `<!-- maintainer: write this -->` markers there instead of drafting prose.

Positioning: "HF first, never HF only". Keeping and verifying what you depend on. Never pitch it as
getting around takedowns, and don't repeat unverified news claims (there is no US ban on open models
as of Sept 2026; NVIDIA's HF acquisition is signed, not closed).
