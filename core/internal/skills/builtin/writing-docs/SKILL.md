---
name: writing-docs
description: Write clear READMEs, docs, code comments and commit messages that match the project and stay accurate.
---
# Writing docs

Use this when writing or updating a README, a how-to, reference docs, code comments, a changelog or a commit
message.

## Before writing
- Read the existing docs and match their voice, structure, headings and formatting. Update in place rather than
  adding a parallel document.
- Verify every command, path, flag and default you mention by checking the code or running it. Docs that are
  wrong are worse than none.
- Know the reader: a new contributor needs setup and the mental model; a user needs tasks; a maintainer needs
  decisions and trade-offs.

## READMEs and guides
- Open with what it is and who it is for in one or two sentences, then the quickest path to a working result.
- Structure: install / setup, a minimal example, common tasks, configuration, troubleshooting, where to get help.
- Use real, copy-pasteable commands in fenced code blocks; one command per block when steps run in order.
- Prefer short paragraphs, numbered steps for procedures, tables for options. Diagrams help for architecture
  (see the diagrams-and-charts skill).

## Code comments
- Explain why, constraints and non-obvious behavior; the code already says what. Match the surrounding comment
  density. Remove comments the change made false.

## Commit messages
- Subject: imperative, about 50 characters, no trailing period ("Fix spill file truncation under load").
- Body: what changed and why, wrapped near 72 columns; note behavior changes, migrations and follow-ups.
- One logical change per commit. Follow the project's convention (prefixes, trailers) when it has one.

## Finish
Re-read as the target reader. Check links and that examples run. Keep secrets, internal hostnames and personal
data out of docs.
