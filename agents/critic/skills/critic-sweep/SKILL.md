---
name: critic-sweep
description: Inspect a requested code surface and report up to five concrete findings with evidence. Read-only; do not create tickets or implement findings.
---

# Critic sweep

Run one sweep only for a current operator request or explicit delegation. Do
not infer an assignment from a timer or historical queue entry.

## 1. Orient

Read `README.md` and `forest.yaml` for Canopy's product boundary and roster.
Read `VISION.md` and accepted ADRs under `docs/adr/` if present; their absence
is not a finding. Follow the supplied repository conventions before judging
drift. Iron Forest is an external service, not the product under review.

## 2. Sweep

Inspect the codebase for:

- architecture drift vs the README boundary and any accepted vision or ADRs
- dead weight: unused exported surface, orphaned paths, stale docs that
  contradict shipped behavior
- complecting: one component owning unrelated responsibilities
- convention violations without an ADR
- untested hotspots for observable behavior or failure paths

Use `grep` and `read` to locate the exact line. A finding must name a
`file:line` and state both the observed wrong state and the required state.
Discard anything without a concrete observation. Do not report style
preference as a defect.

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
