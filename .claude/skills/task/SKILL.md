---
name: task
description: Pick up and complete the next task (or a named task like "M1.3") from docs/roadmap.md, end to end, step by step. Use when the user says "next task", "continue", "work on M2.4", or asks to keep going through the roadmap.
argument-hint: "[task id, e.g. M1.3]"
---

# Work a roadmap task

Open tasks: !`grep -n -E '^- \[ \] \*\*M[0-9]+\.[0-9]+' docs/roadmap.md | head -8`

## 1. Choose

If `$ARGUMENTS` names a task, take it. Otherwise take the first unchecked task whose dependencies
(listed as "after M1.2") are checked. Tell the user which task and why in one line.

## 2. Understand before writing

- Re-read the task's acceptance criteria in `docs/roadmap.md`.
- Read the relevant section of `docs/design.md` and any ADR the task names.
- If the task touches the proxy or Hub client, load the hf-protocol skill. Licences: licence-policy.
- Read the existing code you will change. Don't restructure what isn't in scope.
- If the task needs a decision the ADRs don't cover, write the ADR first (adr skill), or ask the user
  if it's a product decision.

## 3. Implement in small steps

- Write the test first when the behaviour is clear (table-driven, `t.TempDir()`).
- Make it pass with the smallest reasonable code. Then clean up.
- Keep the package dependency order from `docs/design.md`.
- Run `make check` after each meaningful step, not only at the end.
- Network tests: guard with `testutil.Network(t)` (skips unless `WEIGHTKEEP_NETWORK_TESTS=1`), tier A
  tiny repos only.

## 4. Verify for real

Unit tests passing is not enough when the task has user-visible behaviour. Build the binary and run the
command the acceptance criteria describe. For proxy work, run a real client against it. Report what you
ran and what happened, including failures.

## 5. Finish

- Tick the checkbox in `docs/roadmap.md` (and add a short note under the task if something changed
  from the plan).
- Update `docs/design.md` or an ADR if the implementation diverged from them.
- Commit with the commit skill. Usually one commit per task; more if the task had separable steps.
- Tell the user: what was done, what was verified and how, anything left open. Then stop and let them
  decide whether to continue, unless they asked you to keep going.
