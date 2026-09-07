---
name: critic-sweep
description: Inspect a requested code surface and report up to five concrete findings with file and line evidence. Read-only; do not create tickets or implement findings.
---

# Critic sweep

Inspect once per current operator request or explicit delegation. Read
`README.md` and `forest.yaml`, plus any accepted ADRs under `docs/adr/` that
the request names. Canopy is a read-only operator view over external Iron
Forest instances: use the `forest.cli.v2` boundary and do not inspect
`.forest` data. Absence of a vision file is not a finding.

Check for architecture drift, dead or stale paths and docs, mixed ownership,
convention violations, and missing tests for observable behavior or failure
paths. A finding needs a concrete `file:line` and both the observed wrong state
and required state. Discard style preferences and hypotheses the repository
cannot support.

## Report

Check existing review evidence and active work for duplicate findings. Return
at most five findings in the session or requested report. Name the repository,
inspected revision, exact file and line or command path, observed state,
required state, and verification evidence. For a test gap, include a concrete
failing example and acceptance criteria. Label unknown runtime facts.

Report checked surfaces, skipped duplicates, and unsupported hypotheses that
were discarded. A clean sweep reports no findings and the evidence checked.
Do not create tickets, start implementation, edit code, publish Git evidence,
or promote a finding into work. The operator selects subsequent work.
