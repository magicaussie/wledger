# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 56
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 034 — Read-Only Predeployment Release Assessment
Production-Authorization: READ_ONLY_ONLY
Target-Main: a12d824e48da7c19b8ad508027898492b9d84c81
Current-Production: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb

## Context
Sequence 55 reports fast-forward merge of .github/workflows/ci.yml and green push-to-main CI run 38045351289. ChatGPT fetched Sequence 55; independent GitHub API query of commit a12d824 returned the PR run successful, while push-run success is documented by DeepSeek in Sequence 55. Production is unchanged. Task 030 dashboard accessibility changes are in target main but not production. User prefers autonomous development; production deployment is NOT authorized by this handoff.

## Read-only tasks
1. Confirm Git origin/main at exact target SHA, current production isolated release path and image/compose provenance at expected production SHA. Inspect current deployment architecture, services and rollback mechanism. No changes.
2. Compare production..target via git diff --name-status and git log --oneline. Expect only Task 030/030B dashboard_wall/grid templ+generated Go, dashboard_render_test.go, eight active locale files, and CI workflow. Explicitly flag any unexpected schema, migration, auth, routing, LED/WLED or dependency changes. Do not infer safety solely from file list; inspect relevant code.
3. Verify CI evidence using gh run view 38045351289 and status of origin/main. Optionally perform local read-only tests in separate checkout, without modifying production checkout.
4. Read-only DB checks: integrity_check, foreign_key_check, goose version, coordinate space, controller/container/mapped-bin counts and physical LED mapping digest. Compare against baseline digest 1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09. Do not expose secrets or session identifiers.
5. Review prior Task 028 deployment process, backup and rollback artifacts, available disk space, current container health, and propose a detailed safe deployment runbook with exact shell commands. For future authorized deployment, require online SQLite backup API, config/uploads snapshot, SHA manifest, pinned images, isolated release, health/API/MCP checks, mapping digest before/after, and clear rollback criteria. Label commands as PROPOSED ONLY; DO NOT RUN them.
6. Describe a non-hardware browser smoke test of dashboard wall modal (prefer staging/isolated local app or user-driven visual check). Never create/modify live walls or operate physical LEDs without explicit authorization.
7. Publish Sequence 57 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 034 with audit findings, target delta, exact proposed commands, hazards, outstanding validation, and recommendation whether ready for separately authorized deployment. STOP.

## Guardrails
Read-only production. No deployment, backups, restores, service restart, database write, merge, push to main, LED/WLED calls, Home Assistant changes, new wall creation, authenticated login/session creation, or original production checkout modifications. Only AI_HANDOFF.md may be committed to experiment/ai-handoff for the report.
