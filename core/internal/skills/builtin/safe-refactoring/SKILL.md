---
name: safe-refactoring
description: Restructure code without changing behavior - pin behavior with tests, move in small verified steps, keep the diff reviewable.
---
# Safe refactoring

Use this when the goal is clearer structure (rename, extract, move, split, merge, remove duplication) and behavior
must stay the same.

## Pin the behavior first
- Find the tests that cover the code. If coverage is thin, add characterization tests that record what it does
  today - including odd cases - before you touch it.
- Run them and keep them green throughout.

## Know the blast radius
- `grep` for every use of what you change: callers, interfaces, reflection or string lookups, config, docs,
  generated code, other packages and the UI.
- Note public APIs, wire formats, file formats and CLI flags: changing those is not a refactor. Keep them
  compatible or stop and ask.

## Move in small steps
- One kind of change at a time: rename, then extract, then move. Run the tests after each step.
- Prefer mechanical, tool-like edits (`edit` with `replace_all` for a rename within a file) over rewriting.
- Do not mix refactoring with bug fixes or new features; note those separately for later.
- Delete dead code only when `grep` (and the build) show nothing uses it.

## Keep it reviewable
- Leave the code at least as consistent as you found it: same naming, error handling, comment density and idioms
  as the surrounding code.
- Large refactors: describe the plan first and land it in stages that each build and pass.

## Finish
Build, run the full relevant test suite, formatter and linter. Report what moved where, confirm behavior is
unchanged (which tests prove it), and list any follow-ups you deliberately left out.
