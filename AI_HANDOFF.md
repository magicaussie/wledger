# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 26
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 021 — Investigate Missing Third Cabinet
Production-Authorization: STRICTLY_READ_ONLY

## User report
User confirms physical LED test works but says there should already be **three cabinets**. Production verification after Task 020 shows one controller, two drawers/containers, 68 bins. Do NOT assume a cabinet equals a drawer or container. Investigate exact domain entities and source of truth.

## Investigation
1. Read AGENTS.md and relevant source, schema/migrations, queries, templates, routing and dashboard views. Determine whether WLEDger distinguishes cabinets, drawers, containers, controllers and how these are represented.
2. Inspect production DB READ ONLY: list relevant records, IDs, names, parent/child relationships, soft-delete/status flags and any third cabinet or orphaned/hidden record. Never print secrets.
3. Compare the current production DB with the fresh pre019 backup, Stage A backup and any existing non-mutating historical evidence to establish whether the third cabinet was ever present, renamed, removed or excluded by filters. Inspect audit logs, migrations/backfill, if useful. Do not restore or change anything.
4. Trace dashboard count/list queries and UI rendering to identify why two are shown. Distinguish missing data from UI/filtering and distinguish physical cabinets from WLED LED strings/segments. If relevant, inspect source history READ ONLY.
5. Report grounded root cause or clearly labeled unresolved possibilities, exact evidence, recommended least-risk remediation, and whether any further information from user is necessary. If records are absent, DO NOT create a placeholder or invent cabinet names, layouts or LED addresses.
6. Publish Sequence 27, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 021, including checks, SQL/query evidence (no sensitive data), root cause and recommended next step. Commit/push only AI_HANDOFF.md on experiment/ai-handoff, no force. STOP.

## Boundaries
STRICTLY READ ONLY: no schema/data edits, migrations, LED/WLED commands, UI Locate, deploy/restart, backup/restore, code changes or Home Assistant changes.
