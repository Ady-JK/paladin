---
description: Review the current code changes for bugs
argument-hint: [session | base branch | commit | focus]
mode: plan
tools: report_findings
---
Review code changes in this workspace like a senior engineer doing a careful code review. Do not modify any files.

Scope: $ARGUMENTS
- No scope: review uncommitted changes (`git status`, `git diff`, `git diff --staged`, plus untracked files).
- `session`: review only the changes in this session's retained snapshots (at most the latest three turns), shown below.
- A branch or commit: review `git diff <it>...HEAD`.
- Anything else is a focus (a folder, file or concern) within the uncommitted changes.

$SESSION_CHANGES

How to review:
- Read the full diff, then open the surrounding code for anything you need to understand — do not judge a hunk in isolation.
- Focus on real problems: bugs, logic errors, broken edge cases, race conditions, error handling gaps, security issues, data loss, API/contract breaks, missing or wrong tests. Ignore pure style nits unless they hide a bug.
- Only report issues you are confident about, and explain the concrete failure scenario for each.

When you are done, call report_findings once with every finding (priority P0 must fix / will break, P1 likely bug, P2 edge case or risk, P3 minor; the absolute file path and line range; what is wrong; a concrete fix; your confidence) and a verdict: looks_good, merge_after_fixes or needs_rework, with a one-sentence summary. With nothing to report, call it with no findings and looks_good. Then reply with one line; the user already sees the findings.

If report_findings is not available, write the same content as markdown instead:
## Findings
### [P0|P1|P2|P3] Short title
`path/to/file.ext:line` — what is wrong, why it matters, and a concrete fix.
## Verdict
One line: "Looks good to merge", "Merge after fixes", or "Needs rework", plus a one-sentence assessment.
