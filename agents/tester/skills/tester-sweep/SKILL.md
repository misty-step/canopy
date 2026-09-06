---
name: tester-sweep
description: Inspect a requested code surface and report up to five concrete findings with evidence. Read-only; do not create tickets or implement findings.
---

# Tester sweep

Run one sweep only for a current operator request or explicit delegation. Do
not infer an assignment from a timer or historical queue entry.

## 1. Orient

Read `README.md` and `forest.yaml` for Canopy's product boundary and roster.
Read `VISION.md` and accepted ADRs under `docs/adr/` if present; their absence
is not a finding. Follow the supplied repository conventions. Identify
configuration, collection failures, freshness, HTTP fragments, and log
presentation as the observable surfaces before looking for gaps.

## 2. Sweep

Find under-tested OBSERVABLE behaviors only:

- boundaries: empty input, empty config, missing values, limits
- transitions: state changes a user can trigger (idle to running, open to
  closed, live to done)
- error paths: invalid CLI form, missing tools, conflicts, and failures users
  actually hit

Never propose implementation-unit tests for internal helpers, and never chase
raw coverage. Use `grep` and `read` to locate the exact surface and its current
tests. A finding must name a specific surface (`file:line` or a concrete
command path) and state both the observed untested behavior and the required
test. Discard anything without that concrete observation.

## Output discipline

Run only for a current operator request or explicit delegation. Return at most
five findings in the session or requested report. Include the repository,
inspected revision, exact file and line or command path, observed state,
required state, and verification evidence. Test gaps need a concrete failing
example and acceptance criteria. Do not create tickets or start implementation.

## Noise control

Check existing review evidence and active work for duplicate findings. Report
checked surfaces, skipped duplicates, and discarded hypotheses. A finding
without a concrete observation is discarded. A clean sweep reports no findings.


For Canopy, distinguish the inspected Git revision from its running build. `forest version` identifies the external Kernel, not Canopy. Label an unavailable running revision as unknown.
