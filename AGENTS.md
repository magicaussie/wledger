# AGENTS.md — WLEDger Development Policy

*** IMPORTANT: You are not yo use any other model that the one that has been prest ***

This file records the development, publication and review policy for WLEDger.
It applies to all contributors and automated agents working in this repository.

## Development-branch publication and independent review

GitHub is the authoritative published source for WLEDger. The authorized development branch is `development/chatgpt-m6-m7`, published to `origin/development/chatgpt-m6-m7`.

1. For each completed, coherent coding task, the lead AI must review the actual changes and run all relevant tests and verification gates before publication. An implementation report alone is not acceptance.
2. Once those reviews and verification gates pass, the lead AI has standing owner authorization to create the task's local commit and push it to `origin/development/chatgpt-m6-m7`; separate permission is not required for each commit or push. This standing authorization is limited to completed work within the approved task scope on that branch. An explicit owner instruction to leave changes uncommitted or unpushed overrides this standing authorization for that task.
3. Never automatically commit or push incomplete, failing, or unreviewed work. Large tasks may be published as coherent, reviewed, and verified checkpoints.
4. Keep application source, tests, migrations, configuration templates, and documentation under version control. Never stage or commit unrelated modifications, secrets, credentials, production databases, user uploads, logs, build artifacts, or generated files that should not be tracked. Do not sweep pre-existing working-tree changes into a task commit.
5. Before committing and pushing, verify the current branch is `development/chatgpt-m6-m7`, confirm the remote and branch alignment and that there is no remote divergence, inspect the exact staged file list, and confirm the relevant test and verification results. Stage only files in the completed task. Never force-push.
6. Stop and ask the owner for approval if the current branch differs from the authorized development branch, the remote has diverged or cannot be safely fast-forwarded, verification fails, or the proposed change exceeds the authorized task scope. Report unrelated changes or possible secrets and do not include them.
7. After publication, report the full commit SHA, parent SHA, changed files, test and verification results, known limitations, and GitHub synchronization status.
8. The owner will provide the published commit SHA to ChatGPT for independent review against the published GitHub source. The independent reviewer must inspect the relevant source files, tests, migrations, and surrounding implementation, and confirm that the implementation matches the reported behavior. Passing tests or a local review do not replace this source review. Address independent review findings before considering the task fully accepted.
9. Publication to the development branch does not authorize merging to `main`, releases, deployments, production database changes or restores, LED-coordinate conversion, or physical WLED operations. Each of those actions requires separate explicit owner authorization.

## Review checkpoints

For small tasks:
Implementation → Lead AI review and verification → Commit → Push to the authorized development branch → Independent GitHub source review → Address findings.

For large tasks:
Coherent implementation checkpoint → Lead AI review and verification → Commit → Push to the authorized development branch → Independent GitHub source review → Address findings → Continue within the approved task scope.

Never force-push any branch. Do not automatically begin another task after publishing a completed task.

---

## OmO orchestration and automatic delegation

This section supplements the development-branch publication, review, safety,
and production policies above. The standing authorization to commit and push
applies only to the lead AI, only after the required review and verification,
and only for completed work within the approved task scope on the authorized
development branch. It does not authorize delegated workers to publish or
expand the task scope.

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

- Commit and push authorization is governed by the development-branch
  publication policy above. No model may merge to `main`, release, deploy,
  modify or restore production databases, convert LED coordinates, or activate
  WLED hardware without separate explicit owner authorization.
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
