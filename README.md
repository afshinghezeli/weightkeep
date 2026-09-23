# weightkeep

Keep verified copies of the open-weight models you depend on, and serve them from a local endpoint
that speaks the Hugging Face Hub API.

<!-- maintainer: write the opening paragraph and the "why" section in your own words -->

## Status

Early development. There is nothing to install yet. The first release (0.1.0) will cover `pull`,
`verify`, `export` and `serve`; progress is tracked in [docs/roadmap.md](docs/roadmap.md).

## How it will work

`weightkeep pull org/model` pins a repository to a commit, downloads every file into a content-addressed
store and checks each one against its SHA-256 (or git blob SHA-1 for small files). `weightkeep serve`
answers the Hub API on `127.0.0.1:8700`, so this works unchanged:

```sh
export HF_ENDPOINT=http://127.0.0.1:8700
python -c "from transformers import AutoModel; AutoModel.from_pretrained('HuggingFaceTB/SmolLM2-135M')"
```

When the Hub has the file, weightkeep fetches it from the Hub. When it doesn't, later versions fall back
to a BitTorrent swarm (with the Hub as a web seed) and to HTTP mirrors, and verify the bytes against the
recorded hashes before serving them. Only models whose licence allows redistribution are ever shared;
gated repos never are.

The details are in [docs/design.md](docs/design.md) and the decision records in [docs/adr](docs/adr).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports from real setups are the most useful thing right now.

## Licence

Apache License 2.0. Model weights are not part of this repository and each model stays under its own
licence.
