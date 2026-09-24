# Changelog

## [0.2.0](https://github.com/afshinghezeli/weightkeep/compare/v0.1.0...v0.2.0) (2026-09-24)


### Added

* **cli:** add seed command ([#22](https://github.com/afshinghezeli/weightkeep/issues/22)) ([28a13e3](https://github.com/afshinghezeli/weightkeep/commit/28a13e36206445618d0108680f963a868f8d95a7))
* **cli:** pull a revision from other nodes over BitTorrent ([#23](https://github.com/afshinghezeli/weightkeep/issues/23)) ([9df38e9](https://github.com/afshinghezeli/weightkeep/commit/9df38e9d7b10cdbf83648ed7d4abecca003b7df1))
* **keep:** fall back to mirrors when the Hub can't serve a revision ([#24](https://github.com/afshinghezeli/weightkeep/issues/24)) ([ea08340](https://github.com/afshinghezeli/weightkeep/commit/ea083406031fda19c72b936acdc77180138f2c60))
* **policy:** decide sharing tiers from licence, gate and base models ([#16](https://github.com/afshinghezeli/weightkeep/issues/16)) ([2031cd0](https://github.com/afshinghezeli/weightkeep/commit/2031cd0130b3e399b2fd6393ca92990fbc6ed9c6))
* **torrent:** build hybrid v1+v2 torrents for a revision ([#17](https://github.com/afshinghezeli/weightkeep/issues/17)) ([829e1b8](https://github.com/afshinghezeli/weightkeep/commit/829e1b83f5e6a876f7ba028b5bae7cf412315a46))
* **torrent:** seed kept revisions straight from the store ([#19](https://github.com/afshinghezeli/weightkeep/issues/19)) ([0e32878](https://github.com/afshinghezeli/weightkeep/commit/0e328780410af554b86714e55e4b7752d573f9a1))


### Documentation

* describe sharing and pulling from peers in the README ([#26](https://github.com/afshinghezeli/weightkeep/issues/26)) ([88e42bb](https://github.com/afshinghezeli/weightkeep/commit/88e42bbd23d0bef5d75e597ee7bb9aaf5a92ad22))

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
