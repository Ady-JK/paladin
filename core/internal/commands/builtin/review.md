---
description: Review the current code changes for bugs
argument-hint: [base branch | commit | focus]
mode: plan
---
Review the current code changes in this workspace like a senior engineer doing a careful code review. Do not modify any files.

Scope: $ARGUMENTS
If no scope is given, review uncommitted changes (`git status`, `git diff`, `git diff --staged`, plus untracked files). If a branch or commit is given, review `git diff <it>...HEAD`.

How to review:
- Read the full diff, then open the surrounding code for anything you need to understand — do not judge a hunk in isolation.
- Focus on real problems: bugs, logic errors, broken edge cases, race conditions, error handling gaps, security issues, data loss, API/contract breaks, missing or wrong tests. Ignore pure style nits unless they hide a bug.
- Only report issues you are confident about, and explain the concrete failure scenario for each.

Output format:
## Findings
For each issue, ordered by severity:
### [P0|P1|P2|P3] Short title
`path/to/file.ext:line` — what is wrong, why it matters, and a concrete fix.

(P0 = must fix / will break, P1 = likely bug, P2 = edge case or risk, P3 = minor.)

## Verdict
One line: "Looks good to merge", "Merge after fixes", or "Needs rework", plus a one-sentence overall assessment. If you found nothing, say so plainly.
