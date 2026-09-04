---
name: tester-sweep
description: >
  Run one behavioral-test cartography sweep and file at most five SPEC-LESS
  draft Powder test-work jobs naming the surface, behaviors, failing-example
  sketch, and acceptance criteria. Read-only: never edit code, never promote
  work, never call a Kernel Effect.
---

# Tester sweep

Run one sweep per dispatch. The poll cadence is set in `forest.yaml`; do not
loop or re-run on your own.

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

## 3. Deduplicate

List current jobs once, using the top-level `repo:` value from `forest.yaml` as the repository:

```sh
powder list --repo <forest.yaml repo>
```

Skip a finding whose surface or proposed test is already covered by an existing
open or draft job.

## 4. File drafts

File at most five findings per sweep. For each, create a job with no spec so
it is never takeable, then attach a note that a Builder can implement
verbatim. The first note is an external draft note: it must carry `filed-by`
and `deployment` provenance before the test-work finding.

```sh
powder create --id <slug> --title '<short title>' --repo <forest.yaml repo>
powder note <slug> --text 'filed-by: <agent-identity> @ <forest.yaml repo>
deployment: <instance> <observed-binary-revision>
Surface: <file:line or command path>. Behaviors: <observable behaviors to test>. Failing example: <concrete input or step that shows the gap>. Acceptance: <criteria a Builder can verify>. Observed: <file:line evidence>. Required: <test required>. Proposed test-work: <one sentence for a Builder>.' --agent "${POWDER_AGENT:-tester}"
```

- `<forest.yaml repo>` is the top-level `repo:` value in `forest.yaml`; never hardcode the repository name.
- `<agent-identity>` is the filing agent (`${POWDER_AGENT:-tester}`).
- `<instance>` identifies the observed Canopy deployment. Record the inspected Canopy Git revision separately from any running build revision; use `unknown` when the latter is unavailable. `forest version` identifies the external Kernel, not Canopy.
- `<slug>` is a short unique id, for example `canopy-tester-<topic>`.
- The note must name the surface, the behaviors to test, a failing-example
  sketch, and acceptance criteria.
- Never pass `--spec` to `powder create`; a spec would make the job takeable.
- Never edit a file, run `forest publish`, `git commit`, or `git push`.

## 5. Report

Summarize the sweep: the surfaces checked, the findings filed (id and surface),
the findings skipped as duplicates, and the findings discarded for lack of
evidence. If nothing was filed, say so and list the evidence you checked.
