---
name: debugging
description: Find and fix a bug systematically - reproduce it, isolate the cause, fix the root, and prove it with a test.
---
# Debugging

Use this when something fails, crashes, hangs or gives a wrong result and the cause is not obvious.

## 1. Reproduce first
- Get the exact failure: the command, input, error text and stack trace. Run it yourself with `shell`; do not
  reason from the user's paraphrase alone.
- Make the smallest reliable reproduction: one test, one command, one input. A failure you cannot trigger on
  demand is not ready to fix.
- If it only fails sometimes, run it in a loop (`for i in $(seq 20); do …; done`) or under load or the race
  detector, and note how often it fails before you change anything.

## 2. Read before you guess
- Read the code on the failing path end to end with `read`; follow calls with `grep` rather than assuming what a
  function does.
- Check recent changes (`git log -p -- <file>`, `git diff`) near the failure: most bugs are new.
- Write down one hypothesis at a time, and the observation that would confirm or kill it.

## 3. Isolate
- Narrow the gap between "works" and "fails": bisect inputs, comment out halves, or `git bisect` across commits.
- Add temporary logging or assertions at the boundary where good data turns bad. Print values, not guesses.
- Distrust coincidences: "it works after I added a print" usually means timing, buffering or ordering.

## 4. Fix the root cause
- Fix where the bad state is created, not where it is noticed. A nil check at the crash site often hides the bug.
- Keep the fix as small as the cause. Do not refactor while fixing.
- Look for the same mistake elsewhere (`grep` for the pattern) and mention any you find.

## 5. Prove it
- Turn the reproduction into a test that fails before the fix and passes after. Run it both ways when practical.
- Run the surrounding test suite, and for flaky bugs repeat the run enough times to be convincing.
- Remove temporary logging.

## Report
State the cause in one or two sentences (what was wrong and why it produced the symptom), the fix, the test that
proves it, and anything you could not verify.
