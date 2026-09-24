# The community registry

The registry is a signed list of revisions: for each `org/name@commit`, the manifest of every file's
size and SHA-256, and optionally a magnet link for the revision's torrent. It also carries a denylist
of repos and files that nobody should help share. It is published as a static
[TUF](https://theupdateframework.io/) repository, so a copy served by anyone can be checked against the
maintainers' keys. The reasoning is in [ADR 0006](adr/0006-registry-trust-model.md).

This page is for two audiences: people who want to use a registry, and whoever runs one.

## Using a registry

A registry is two things: a URL where its files are served, and its trusted root, `1.root.json`, which
you get once from a source you trust (the registry repo's README, say) and keep locally.

```toml
# ~/.config/weightkeep/config.toml
[registry]
url = "https://example.org/weightkeep-registry"
root = "/home/me/.config/weightkeep/registry-root.json"
```

or `WEIGHTKEEP_REGISTRY_URL` and `WEIGHTKEEP_REGISTRY_ROOT`. Then:

```console
$ weightkeep registry sync
registry synced: 1 revision record(s), 0 denylist entr(ies)
$ weightkeep registry show prajjwal1/bert-tiny
prajjwal1/bert-tiny@6f75de8b60a9  5 files, 18.0 MB  added 2026-09-24
```

`sync` downloads every record and checks it against the signed metadata. After that, lookups work
offline until the timestamp expires (seven days), when another `sync` is needed. The client rejects:

- metadata not signed by the keys the trusted root names;
- a record whose bytes don't match the hash the signed metadata lists;
- metadata older than what it has already seen (a mirror rolling the registry back);
- expired metadata (a mirror freezing it, for example to hide a new denylist entry).

### What the denylist does

With a registry configured, weightkeep treats a denylisted revision as tier C:

- `weightkeep seed` skips it, giving the entry's reason. `seed` syncs the registry when it starts and
  refuses to run if it has no current denylist, so an expired copy can't hide a new entry.
- `weightkeep pull --torrent` refuses it as soon as the torrent's manifest arrives, before downloading
  any file data.
- `weightkeep serve`, when listening beyond localhost, answers `451` with `X-Error-Code: Denylisted`
  to other machines, whether they ask by repo name or by blob digest. Clients on the same machine
  still get it: what you already keep for yourself stays yours to use.

A `seed` that is already running keeps the denylist it started with; restart it after a sync to pick
up new entries.

## Submitting a revision

Pull the revision, then print its record:

```sh
weightkeep pull prajjwal1/bert-tiny
weightkeep registry record prajjwal1/bert-tiny > 6f75de8b60a9f8a2fdf7b69cbd86d9e64bcb3837.json
```

If you seed it, add `--magnet` with the link `weightkeep seed` printed. Open a pull request against the
registry repo that adds the file at `records/models/<org>/<name>/<commit>.json`.

CI then checks it against the Hub. It downloads the tree at that commit and requires the record to list
exactly the Hub's files, with matching sizes, git ids and SHA-256 (LFS files by the hash the Hub lists,
regular files by downloading and hashing them). The repo must be neither gated nor private, its
licence must be tier A or B (see [ADR 0007](adr/0007-licence-tiers.md)), and nothing in it may be on the
denylist. A record for a revision the Hub no longer serves can't pass this check, which is deliberate:
the registry vouches only for what someone could still compare with the source.

## Running a registry

The maintainer tool is a separate binary, `weightkeep-registry`:

```sh
go install github.com/afshinghezeli/weightkeep/cmd/weightkeep-registry@latest
```

### Layout

The registry repo holds the sources. The published site is generated from them.

```
records/models/<org>/<name>/<commit>.json   one record per revision
denylist.json                               {"version": 1, "entries": [...]}
```

A denylist entry names a repo (`"repo": "org/name"`), one commit of it (`"repo"` and `"commit"`), or
content anywhere (`"sha256"`), always with a `"reason"` and an `"added"` date.

### Creating it

```console
$ weightkeep-registry init --keys ./keys --out ./pub
wrote keys to ./keys and pub/metadata/1.root.json
```

This makes one ECDSA P-256 key per role in `./keys` (`root-1.key`, `targets-1.key`, `snapshot-1.key`,
`timestamp-1.key`, mode 0600) and signs the first root. `--root-keys 3 --threshold 2` makes three root
keys of which two must sign. Commit `pub/metadata/1.root.json` to the repo and link it from the README:
it is what users trust.

Key custody:

- The root keys stay offline, on separate machines or hardware if there are several maintainers. They
  are needed only to rotate the other keys.
- The targets, snapshot and timestamp keys go into the repo's CI secrets, because CI signs every
  publish and re-signs the timestamp daily.

That split means a compromised CI can sign bad records until someone notices, but can't lock the
maintainers out: the root keys can replace every CI key. `weightkeep-registry` doesn't rotate keys
yet; until it does, rotation means editing and re-signing `root.json` with another TUF tool such as
`tuf` from go-tuf.

### Checking and building

```console
$ weightkeep-registry check records/models/prajjwal1/bert-tiny/6f75de8b60a9f8a2fdf7b69cbd86d9e64bcb3837.json
ok    records/models/prajjwal1/bert-tiny/6f75de8b60a9f8a2fdf7b69cbd86d9e64bcb3837.json  tier A (MIT License), 5 files
$ weightkeep-registry build --keys ./keys --src . --out ./pub
built ./pub: 1 record(s), 0 denylist entr(ies)
```

`build` needs the previously published `pub/metadata` in place, so each publish gets higher version
numbers than the last. Clients that saw version 5 refuse a mirror still serving version 4.

### CI

These workflows are a starting point for the registry repo; adapt the publishing step to wherever the
site is hosted. The keys are stored as secrets holding the PEM files.

```yaml
# .github/workflows/check.yml
on: pull_request
permissions: {contents: read}
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with: {fetch-depth: 0}
      - uses: actions/setup-go@v6
        with: {go-version: stable}
      - run: go install github.com/afshinghezeli/weightkeep/cmd/weightkeep-registry@latest
      - name: Check added or changed records
        run: |
          files=$(git diff --name-only --diff-filter=AM origin/${{ github.base_ref }}... -- 'records/*.json')
          [ -z "$files" ] || weightkeep-registry check $files
```

```yaml
# .github/workflows/publish.yml
on:
  push: {branches: [main]}
  schedule: [{cron: "17 4 * * *"}]   # re-sign the timestamp daily
permissions: {contents: write}
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
      - uses: actions/checkout@v5
        with: {ref: gh-pages, path: pub}
      - uses: actions/setup-go@v6
        with: {go-version: stable}
      - run: go install github.com/afshinghezeli/weightkeep/cmd/weightkeep-registry@latest
      - name: Sign
        env:
          TARGETS_KEY: ${{ secrets.TARGETS_KEY }}
          SNAPSHOT_KEY: ${{ secrets.SNAPSHOT_KEY }}
          TIMESTAMP_KEY: ${{ secrets.TIMESTAMP_KEY }}
        run: |
          umask 077 && mkdir -p "$RUNNER_TEMP/keys"
          printf '%s\n' "$TARGETS_KEY" > "$RUNNER_TEMP/keys/targets-1.key"
          printf '%s\n' "$SNAPSHOT_KEY" > "$RUNNER_TEMP/keys/snapshot-1.key"
          printf '%s\n' "$TIMESTAMP_KEY" > "$RUNNER_TEMP/keys/timestamp-1.key"
          if [ "${{ github.event_name }}" = schedule ]; then
            weightkeep-registry timestamp --keys "$RUNNER_TEMP/keys" --out pub
          else
            weightkeep-registry build --keys "$RUNNER_TEMP/keys" --src . --out pub
          fi
      - name: Push the site
        working-directory: pub
        run: |
          git config user.name "registry bot"
          git config user.email "registry-bot@users.noreply.github.com"
          git add -A && git commit -m "publish $(date -u +%FT%TZ)" && git push
```

## Limits

- Every client downloads every record on `sync`. That is fine for thousands of revisions. Past that,
  the plan is TUF delegations per namespace, so clients fetch only the parts they look up.
- The magnet link in a record is a hint. `weightkeep pull --torrent` verifies what it downloads against
  the manifest inside the torrent, and that manifest must match the registry's record.
- The registry says what a revision's files hashed to when it was checked. It says nothing about
  whether the model is safe to run.
