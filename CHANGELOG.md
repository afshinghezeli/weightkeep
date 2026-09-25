# Changelog

## [0.2.1](https://github.com/afshinghezeli/weightkeep/compare/v0.2.0...v0.2.1) (2026-09-25)


### Added

* **manifest:** sign and verify revisions as OpenSSF Model Signing bundles ([#27](https://github.com/afshinghezeli/weightkeep/issues/27)) ([4b6f6a4](https://github.com/afshinghezeli/weightkeep/commit/4b6f6a4650c3438319a1913f87f6463e58eb948d))
* **registry:** sign, check and sync a community registry ([#29](https://github.com/afshinghezeli/weightkeep/issues/29)) ([933b3c9](https://github.com/afshinghezeli/weightkeep/commit/933b3c9eee70359a0ba7f10b02b3fc2b4ca1e5f3))

## [0.2.0](https://github.com/afshinghezeli/weightkeep/compare/v0.1.0...v0.2.0) (2026-09-24)

Sharing. Kept revisions can now be seeded over BitTorrent, where their licence allows it, and pulled
from other weightkeep nodes when the Hub can't serve them, by torrent or through HTTP mirrors.

### Added

* `weightkeep seed` shares fully kept revisions straight from the store, with the Hub as a web seed,
  an upload rate limit and a monthly cap. Every revision is listed with the reason it is or isn't
  shared. ([#19](https://github.com/afshinghezeli/weightkeep/pull/19),
  [#22](https://github.com/afshinghezeli/weightkeep/pull/22))
* `weightkeep license REPO` shows the sharing tier a revision gets and why: gated, private and
  unlicensed repos are never shared; tier B licences (Llama, Gemma, OpenRAIL, ...) need an explicit
  `--allow`, non-commercial ones also `--non-commercial`. When a permissively licensed repo has no
  LICENSE file, the licence text travels with the torrent.
  ([#16](https://github.com/afshinghezeli/weightkeep/pull/16))
* `weightkeep pull --torrent MAGNET [--peer HOST:PORT]` fetches a revision from other nodes. The
  torrent carries the revision's manifest inside its info dict, so a magnet link alone is enough to
  verify every file. ([#23](https://github.com/afshinghezeli/weightkeep/pull/23))
* A `mirrors` list (config or `WEIGHTKEEP_MIRRORS`) is tried when the Hub is down or no longer has a
  repo; any node's `weightkeep serve` works as a mirror. Never used for gated repos, and mirrors never
  receive your Hub token. ([#24](https://github.com/afshinghezeli/weightkeep/pull/24))

### Notes

* Torrents are BitTorrent v1 with padded files, which qBittorrent, Transmission and libtorrent download
  too. Hybrid v1+v2 torrents are built and tested but not seeded yet: anacrolix/torrent and libtorrent
  disagree on piece lengths for them (ADR 0010).
* Downloading from the Hub as a web seed was tested for 76 minutes, past the expiry of the Hub's
  signed CDN URLs.

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
