#!/usr/bin/env bash
# PostToolUse hook: gofmt Go files Claude just wrote, and report vet-level syntax errors.
set -euo pipefail
f=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))')
case "$f" in *.go) ;; *) exit 0 ;; esac
[ -f "$f" ] || exit 0
if ! out=$(gofmt -l -w "$f" 2>&1); then
  echo "gofmt failed on $f:" >&2
  echo "$out" >&2
  exit 2
fi
exit 0
