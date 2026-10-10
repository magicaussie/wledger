# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 28
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 022 — Functional Audit and Prioritized Next Work
Production-Authorization: READ_ONLY_NO_CHANGES
Expected-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55

## Context
Tasks 019/020: successful segment-relative to drawer-relative conversion, verified 68/68 mappings, physical LED locate works. Task 021: user confirms one WLED controller with two strings is intentional, so NO missing cabinet issue. User asks what to do next. Audit before new feature work.

## Scope
1. Read AGENTS.md, project status/README/docs, existing tests, open TODOs, routes, schemas, and relevant recent history. Establish actual implemented features vs intended ones. Verify branch and production state; do not assume historic claims are current.
2. Perform non-mutating functional audit of main user workflows: dashboard, controller/segment/drawer/bin management, inventory/parts/assignments, search and locate (code/tests only; no physical LED commands), QR labels, authentication/authorization, API/MCP, Home Assistant interface, backup/restore readiness, and responsive UI. Assess reliability/security/data integrity, including dangerous automatic actions and accidental production writes.
3. Run available test suites and static checks in isolated development environment, without touching production data, hardware or services. Identify reproducible failures with precise file/line/test evidence. If UI cannot be authenticated without user session, mark untested rather than bypassing auth.
4. Produce prioritized findings (critical/high/medium/low), clear repro steps and risk, existing coverage gaps, and a short next 3-task plan. Distinguish verified defects from suggestions and unverified hypotheses. Consider previously observed tiny/overlapping text in dashboard string tiles as a possible UI issue requiring evidence.
5. Publish Sequence 29, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 022 with summary, tests, concrete references, prioritized next tasks and any blockers. Commit/push ONLY AI_HANDOFF.md on experiment/ai-handoff without force; STOP.

## Boundaries
STRICTLY NO SOURCE OR PRODUCTION CHANGES, no migrations, no DB writes, no deploys/restarts, no backup/restore, no LED/WLED commands, no Home Assistant changes. User's one-controller/two-string layout is correct; do not propose a third cabinet as a fix. Do not expose secrets.
