---
description: Generate or update AGENTS.md for this project
argument-hint: [focus]
---
Create or improve an `AGENTS.md` file at the root of the current workspace so future coding-agent sessions start with the knowledge they need.

1. Explore first: read the README, manifests (package.json, go.mod, pyproject.toml, Cargo.toml, Makefile, CI workflows), and the top-level layout. If an AGENTS.md (or CLAUDE.md) already exists, read it and improve it instead of starting over — keep anything still accurate.
2. Write a concise AGENTS.md (aim for under ~150 lines) covering:
   - What the project is, in two or three sentences.
   - Repository layout: the important directories and what lives in each.
   - Exact commands to install, build, lint, type-check, and run tests — including how to run a single test.
   - Code style and conventions that are not obvious from linters (naming, error handling, module boundaries, test patterns).
   - Gotchas: generated files, required env vars, services that must be running, things that must never be committed.
3. Only state facts you verified in the repository. Do not invent commands.
4. When done, summarize what you wrote and anything you could not determine.

$ARGUMENTS
