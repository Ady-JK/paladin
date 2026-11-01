---
name: code-review
description: Review a change like a careful senior engineer - correctness, edge cases, concurrency, errors, tests and API contracts, with concrete fixes.
---
# Code review

Use this to review a diff, a branch, a pull request or a file. (`/review` runs a structured review with a findings
card; this skill is the checklist behind it, usable in any turn.)

## Get the full picture
- Read the whole diff (`git diff`, `git diff --staged`, `git diff <base>...HEAD`), then open the surrounding code
  for anything you need to judge a hunk: callers, the type definitions, the tests.
- Understand the intent: the commit message, the linked task, the tests that were added.

## Check, in order of cost of a miss
1. **Correctness**: does it do what it claims for every input? Off-by-one, wrong operator, inverted condition,
   unhandled enum value, wrong units, integer overflow, float comparison, time zones.
2. **Edge cases**: empty, nil/null, zero, one, many, maximum, duplicate, unicode, very long, concurrent calls,
   first run and upgrade from old data.
3. **Errors**: are failures checked, propagated with context, and cleaned up (files, locks, goroutines,
   processes)? Are partial writes possible? Is anything swallowed?
4. **Concurrency**: shared state without a lock, check-then-act races, channel close/send races, goroutine or
   timer leaks, ordering assumptions, deadlocks.
5. **Security**: untrusted input reaching shell, SQL, paths, HTML or URLs; secrets in logs; authorization checks.
   (For a deeper pass use the security-review skill.)
6. **Contracts**: public APIs, wire and file formats, config keys, CLI flags - are they compatible? Are docs and
   callers updated?
7. **Tests**: do they fail without the change? Do they cover the edges above? Are they deterministic?
8. **Simplicity**: dead code, duplication with existing helpers, needless abstraction.

## Report
- Only issues you are confident about, each with a concrete failure scenario ("when X, Y happens because Z") and a
  concrete fix. Mark priority: P0 will break, P1 likely bug, P2 edge case or risk, P3 minor.
- Skip pure style nits unless they hide a bug.
- End with a verdict: looks good, merge after fixes, or needs rework, plus one sentence why.
