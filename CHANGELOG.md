# Changelog

## 0.1.0 (2026-09-24)

First release. weightkeep keeps commit-pinned, hash-verified copies of Hugging Face model repositories
and serves them through a local endpoint that speaks the Hub API, so existing tools keep working when
the Hub doesn't answer.

### Added

* `weightkeep pull REPO[@REV]` downloads a revision into a content-addressed store and checks every
  file against the hash the Hub lists for it. Downloads resume after interruptions; `--include` and
  `--exclude` pick which large files to fetch. ([#7](https://github.com/afshinghezeli/weightkeep/pull/7))
* `weightkeep serve` answers the Hub API on `127.0.0.1:8700`. Tested with huggingface_hub 1.32 (with
  `hf_xet` installed), transformers, llama.cpp's `-hf` (build 10964), Ollama 0.34 and
  text-generation-webui's downloader. Files not kept yet are fetched on first request, verified and
  streamed; kept revisions are served when the Hub is unreachable, or always with `--offline`.
  ([#11](https://github.com/afshinghezeli/weightkeep/pull/11),
  [#12](https://github.com/afshinghezeli/weightkeep/pull/12),
  [#14](https://github.com/afshinghezeli/weightkeep/pull/14))
* `weightkeep ls` and `weightkeep verify`; verify moves corrupt blobs to `quarantine/` so the next pull
  replaces them. ([#8](https://github.com/afshinghezeli/weightkeep/pull/8))
* `weightkeep export` writes a kept revision into the Hugging Face cache (or a plain directory) for
  `HF_HUB_OFFLINE=1` use, reflinking where the filesystem allows.
  ([#9](https://github.com/afshinghezeli/weightkeep/pull/9))
* `weightkeep rm` and `weightkeep gc` to forget revisions and free space.
  ([#10](https://github.com/afshinghezeli/weightkeep/pull/10))
* `weightkeep env` prints the resolved paths, upstream and token source (never the token).
  ([#2](https://github.com/afshinghezeli/weightkeep/pull/2))

### Known limits

* Files over 50 GB can't be served to huggingface_hub over plain HTTP.
* `serve` has no authentication; it binds to localhost by default.
* Models only: datasets and Spaces aren't supported yet.
