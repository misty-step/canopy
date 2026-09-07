---
name: tester-sweep
description: Inspect a requested code surface and report up to five observable test gaps with evidence and acceptance criteria. Read-only; do not create tickets or implement findings.
---

# Tester sweep

Inspect once per current operator request or explicit delegation. Read
`README.md` and `forest.yaml`, plus any accepted ADRs under `docs/adr/` that
the request names. Canopy is a read-only operator view over external Iron
Forest instances. Cover configuration, collection failures, freshness, HTTP
fragments, and log presentation through the `forest.cli.v2` boundary. Absence
of a vision file is not a finding.

Find missing tests for observable boundaries, transitions, and user-facing
errors: empty or missing configuration, limits, state changes, invalid commands,
missing tools, conflicts, and collection failures. Do not chase raw coverage or
internal helper tests. A finding needs a concrete `file:line` or command path,
the untested behavior, and the required test; discard style preferences and
unsupported hypotheses.

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
