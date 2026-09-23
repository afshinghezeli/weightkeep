# Contributing

Thanks for looking. weightkeep is young, so the most useful contributions right now are bug reports
from running it against real models and real clients, and small focused PRs.

## Before you start

- For anything bigger than a bug fix, open an issue or a discussion first so we can agree on the
  approach. It saves you from writing a PR that can't be merged.
- Changes to the store layout, the manifest format, the proxy's wire behaviour or the licence policy
  need an ADR. See [`docs/adr/`](docs/adr/).

## Building

You need Go 1.26 or newer and `make`.

```sh
make build      # ./bin/weightkeep
make test       # unit tests
make lint       # golangci-lint
make check      # everything CI runs, except the client compatibility tests
```

The client compatibility tests run real `huggingface_hub` against `weightkeep serve`. They need
[uv](https://docs.astral.sh/uv/) and network access:

```sh
make compat
```

Tests that touch the network are skipped unless `WEIGHTKEEP_NETWORK_TESTS=1` is set. They only ever
use small, Apache-2.0 or MIT licensed repos. Please keep it that way; never add a gated or restricted
model to tests, docs or examples.

## Code

- `gofmt` and `golangci-lint` clean. `make lint` runs both.
- Errors are wrapped with context (`fmt.Errorf("open blob %s: %w", sum, err)`), returned, not logged
  and returned.
- No panics outside `main` and tests.
- Packages under `internal/` follow the dependency order in [`docs/design.md`](docs/design.md#packages).
- Tests live next to the code, are table-driven where it helps, and use `t.TempDir()` for files.
- Anything that writes to the store goes through `internal/store`.

## Commits and pull requests

Commit messages and PR titles follow Conventional Commits. The short version:

```
fix(proxy): send X-Repo-Commit on 404 responses
```

The full rules, including scopes and what counts as a breaking change, are in
[`docs/contributing/commits.md`](docs/contributing/commits.md). PRs are squash-merged and the title becomes
the commit on `main`, so CI checks the title.

In the PR description, say what changed, why, and how you tested it.

## Documentation

Write plainly. Say what the thing does and what it doesn't. Prefer an example to an adjective.
The style notes in [`docs/contributing/style.md`](docs/contributing/style.md) apply to the README, docs and
user-facing messages.

## AI-assisted contributions

Using an AI tool is fine. What matters is that you understand every line you submit and can answer
review questions about it yourself.

- Say in the PR description which tool you used and for what.
- Don't submit code you haven't run.
- Don't use an AI to write issue reports, review comments or discussion replies on your behalf.

PRs that look generated without that care will be closed without a detailed review. The maintainer
uses Claude Code on this project too; the project's instructions for it are in `CLAUDE.md` and
`.claude/`, in the open.

## Licence

By contributing you agree that your contribution is licensed under the Apache License 2.0, the same
as the rest of the project. Model weights are never part of this repository and are not covered by
its licence.
