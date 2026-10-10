# AGENTS.md — WLEDger Development Policy

This file records the development, publication and review policy for WLEDger.
It applies to all contributors and automated agents working in this repository.

## GitHub synchronization and technical review

GitHub is the authoritative published source for WLEDger.

1. Keep the full application source, tests, migrations, configuration templates and documentation under version control.
2. Never commit secrets, production databases, user uploads, logs, credentials, build artifacts or model files.
3. At the end of each coherent development task, run the relevant tests and create a local commit.
4. Publish approved commits to origin/main after verifying a clean working tree and fast-forward compatibility.
5. For substantial tasks, create intermediate review checkpoints rather than accumulating large unreviewed changes.
6. After each published task, report:
   - Full commit SHA
   - Parent SHA
   - Changed files
   - Test results
   - Known risks
   - GitHub synchronization status
7. Provide the technical lead with the commit SHA so ChatGPT can independently inspect the published source through GitHub.
8. Do not treat an implementation report or passing tests as a substitute for independent source review.
9. Do not deploy, modify production databases, perform restores or convert LED coordinates merely because a commit was pushed.
10. Production operations always require separate explicit authorization.
11. Independent reviews must inspect relevant source files, tests, migrations and surrounding implementation directly from the published GitHub commit. Confirm that the changes match the reported behaviour and identify any discrepancies.

## Review checkpoints

For small tasks:
Implementation → Tests → Local commit → Approved push → GitHub source review.

For large tasks:
Implementation checkpoint → Tests → Commit → Approved push → GitHub review → Continue.

Use a review branch when publishing work that is not yet approved for main. Never force-push main.

## Task boundaries

Do not automatically begin another task after publishing a completed task.

Stop and report any unexpected branch divergence, failing tests, unrelated changes or possible secrets.

---

## OmO orchestration and automatic delegation

This section supplements every existing instruction above; it does not replace
the safety, GitHub synchronization, review, publication or production policies.
Explicit user authorization determines which workflow steps may be performed.
The standing commit and publication checkpoints are not authorization by
themselves: when authorization is absent or the user prohibits those actions,
stop before them and report the reviewable working-tree changes.

### Responsibilities

- GPT-6.1 Sol is the lead architect, orchestrator and independent reviewer. Sol
  retains responsibility for architecture, security, data preservation,
  migration contracts and final acceptance.
- DeepSeek V4.1 Flash is the automatic delegated implementation worker for
  bounded, routine coding tasks.
- Sol delegates suitable work automatically through OmO within the approved
  task scope, without requesting user approval for each routine delegation.
  Delegation does not authorize a new task or expand the approved scope.

### Delegation contract and acceptance

Every delegated task must define:

1. The objective and bounded scope.
2. The permitted files and forbidden changes.
3. Observable acceptance criteria and a stop condition.
4. Required tests and verification commands.
5. Prohibited operations and the applicable authorization limits.

DeepSeek must return implementation evidence: changed files, a description of
the changes, tests and commands run with their results, failures, unrun checks,
uncertainties and any remaining blockers. A worker report is not acceptance.

Before accepting delegated work, Sol must independently inspect the actual
source changes and surrounding implementation, verify the relevant tests and
results, and resolve or report failures and uncertainties. Existing independent
review of published GitHub source remains required; local review does not
replace that checkpoint.

### Authorization and architecture safeguards

- No model may commit, push, deploy, modify production databases or activate
  WLED hardware without explicit user authorization for the action. Approval
  to edit files or delegate routine work grants none of these permissions.
- Existing production, restore and LED-coordinate conversion restrictions
  remain in force. Historical permissions are not current authorization.
- Legacy containers represent electrical strings/segments, not physical
  drawers. Do not repurpose them by renaming.
- Support arbitrary cabinet sizes, drawer and bin counts, LED strings and
  wiring paths. The owner's 69 drawers are one installation, not a schema
  limit or hardcoded topology; the historical 68 mapped bins remain unreconciled.
- Keep physical drawer/bin geometry, LED mounting and electrical mapping
  separate. Each drawer must support multiple LED ranges.
- Preserve stable inventory identities and quantities through layout,
  numbering, mounting and mapping changes.

### Active development handover

`docs/OMO_HANDOVER.md` is the single active development handover document.
Record the Git baseline, current authorization, task status, architecture
invariants, decisions, risks, delegated work and verification evidence there.
`AGENTS.md` remains development policy and `ROADMAP.md` remains product design.

Preserve `experiment/ai-handoff` and Task 044 / Sequence 77 as unapproved
archival evidence. Do not continue its numbered ChatGPT/DeepSeek exchange or
treat its proposals as implementation authorization.
