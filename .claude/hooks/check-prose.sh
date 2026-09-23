#!/usr/bin/env bash
# PostToolUse hook: flag filler words and emoji headings in Markdown files.
# Reads the hook payload on stdin. Exit 2 sends the findings back to Claude.
# Also usable by hand: .claude/hooks/check-prose.sh FILE...
set -euo pipefail

if [ "$#" -gt 0 ]; then
  files=("$@")
else
  f=$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))')
  files=("$f")
fi

status=0
for file in "${files[@]}"; do
  case "$file" in
    *.md) ;;
    *) continue ;;
  esac
  # Third-party texts we don't edit.
  case "$file" in
    *CODE_OF_CONDUCT.md|*CHANGELOG.md|*.notes/*|*reference.md) continue ;;
  esac
  [ -f "$file" ] || continue

  words='seamless|robust|leverag(e|es|ing)|cutting-edge|state-of-the-art|blazing(ly)?|supercharg|effortless|elevate|empower|unlock(s|ing)?|streamlin|harness(es|ing)?|delve|tapestry|testament|pivotal|game-?changer|revolutioni[sz]e|revolutionary|next-generation|holistic|in today.s|look no further|it.s worth noting|at its core|dive in|deep dive|whether you.re'
  hits=$(grep -n -i -E "\\b($words)" "$file" | grep -v -E '^[0-9]+:\s*(```|    )' || true)
  starts=$(grep -n -E '(^|\. )(Additionally|Moreover|Furthermore),' "$file" || true)
  emoji=$(grep -n -P '^#{1,6} .*[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]' "$file" 2>/dev/null || true)

  # style.md lists the words on purpose.
  case "$file" in *docs/contributing/style.md|*.claude/skills/docs-voice/*) hits=""; starts="" ;; esac

  if [ -n "$hits$starts$emoji" ]; then
    status=2
    {
      echo "prose check: $file"
      [ -n "$hits" ] && echo "$hits" | sed 's/^/  filler word, line /'
      [ -n "$starts" ] && echo "$starts" | sed 's/^/  sentence opener, line /'
      [ -n "$emoji" ] && echo "$emoji" | sed 's/^/  emoji heading, line /'
      echo "  see docs/contributing/style.md; rewrite with something specific"
    } >&2
  fi
done
exit $status
