---
name: test-driven-development
description: Make a change test-first - write a failing test for the behavior, make it pass with the simplest code, then clean up.
---
# Test-driven development

Use this for new behavior and bug fixes where a test can express what "done" means.

## Before writing code
- Find how the project tests: the test folder, naming, framework and the command that runs one test
  (`go test ./pkg -run TestX`, `pytest path::test_x`, `npx vitest run file`, `npx playwright test file`).
- Read one or two nearby tests and copy their style, helpers and fixtures. Do not invent a new test style.

## The loop
1. **Red**: write one small test for the next piece of behavior. Name it after the behavior, not the function
   (`TestExpiredTokenIsRejected`). Run it and watch it fail for the right reason - a missing feature, not a typo
   or an import error.
2. **Green**: write the simplest code that makes it pass. Resist adding what the test does not ask for.
3. **Refactor**: with the test green, remove duplication and tidy names. Run the tests again.
Repeat until the feature is complete.

## What to test
- The happy path, the edges (empty, zero, one, many, limits, unicode, very long), and the failure paths (bad
  input, missing file, timeout, permission denied).
- Behavior through public interfaces, not private details, so refactors do not break tests.
- For bugs: the exact reproduction first, so the test proves the fix.
- For concurrency: run under the race detector or repeat the test; a single pass proves little.

## Good tests are
- Deterministic: no real network, no wall-clock sleeps where a signal or fake clock works, no order dependence.
- Fast and isolated: temp dirs, fakes for external services, no shared global state left behind.
- Readable: arrange, act, assert; one reason to fail; a failure message that says what was expected.

## Finish
Run the focused tests, then the wider suite, plus the formatter and linter the project uses. Report which tests
you added, that they failed before and pass now, and anything left untested.
