# Security policy

weightkeep's job is to hand you the bytes you expected. A bug that lets it serve, seed or accept a
file that does not match its recorded hash is a security bug, and so is anything that leaks a Hugging
Face token.

## Reporting

Please use GitHub's private vulnerability reporting:
[Report a vulnerability](https://github.com/afshinghezeli/weightkeep/security/advisories/new).

Include the version (`weightkeep version`), your OS, and the steps or a proof of concept. You will get
an acknowledgement within 5 days. Once a fix is released, the advisory is published with credit unless
you ask otherwise.

Please do not open a public issue for security problems.

## Supported versions

Until 1.0, only the latest release gets fixes.

## In scope

- Hash verification bypasses in the store, fetcher, proxy or torrent code.
- Path traversal through repo ids, revisions or file paths (including symlinks in HF cache exports).
- Token leakage: sending `Authorization` to a host other than the configured Hub, logging tokens.
- The proxy serving gated or private content that was fetched with someone else's token.
- Signature or TUF verification flaws in the registry client.

## Out of scope

- Malicious model weights. weightkeep verifies that bytes are the bytes that were published; it does
  not judge whether they are safe to load. Prefer safetensors and GGUF over pickle.
- Denial of service against `weightkeep serve` when it is exposed beyond localhost. It binds to
  `127.0.0.1` by default for that reason.
