# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 16
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 016 — Deploy Manual-Only Drawer Locate Fix
Production-Authorization: CONTROLLED_DEPLOYMENT_OF_APPROVED_FIX_ONLY
Target-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Previous-Production-Commit: 63016f4e75134ae0675934818fb77f53dd463800
Existing-Backup: /home/spetchal/backups/wledger-20261010T040208Z

## User authorization
User replied "go" to preparing the small production deployment after the manual-only drawer Locate fix was independently reviewed and merged into main via PR #1. Authorization is limited to deploying exactly this approved fix and safe verification. No manual LED conversion, other features, DB manipulation, or physical LED tests are authorized.

## Preflight — stop on discrepancy
1. Read AGENTS.md. Verify origin/main == target commit above; verify it contains only approved PR #1 changes relative to previous production commit. Verify current production deployment, image IDs, containers, environment and bind mounts. Do not touch dirty original checkout.
2. Verify production DB goose version 10, integrity_check, foreign_key_check, counts, flags, allocations, and LED mapping digest (prior: 1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09). No schema migration is expected; STOP if an unexpected migration would run.
3. Verify Stage A backup integrity and retained rollback images. Since Stage A predates the Stage B migration, ALSO create a fresh WAL-consistent, verified, restricted backup of the current goose-v10 production DB before replacing containers; keep outside repo, hash and verify it. Protect secrets and do not disclose contents.
4. Verify release directory strategy: use a NEW clean pinned release checkout (or equivalent immutable isolated release) with symlink/bind mounts resolving to existing production data/uploads/logs and existing secure .env. Do not reset, clean, or overwrite original production checkout or previous release.

## Authorized execution
5. Build exactly target commit and recreate only the WLEDger service(s) required to deploy the drawer page change; avoid unnecessarily recreating MCP. Keep compose project/container names, existing data, network and security configuration. Brief service interruption is authorized.
6. Perform non-destructive verification: release HEAD and running image ID, container health/restarts, application/proxy login HTTP, API/MCP auth where applicable, logs, DB integrity/FK/goose 10/counts/flags/digest/allocations unchanged. Inspect rendered template/source or offline test to confirm drawer page has no load-triggered POST and explicit Locate button remains.
7. Do NOT click Locate, open an authenticated drawer page if it auto-triggers hardware under any uncertainty, send any LED/WLED command, convert LED coordinates, or change DB data. Authenticated UI tests only if safe existing session; otherwise report NOT TESTED.
8. If unexpected change or failure: STOP, preserve evidence, and report. A safe container-image rollback to the immediately preceding deployment is allowed if necessary and verified; never restore an older DB over new data without separate authorization. No speculative fixes.

## Reporting
9. Replace AI_HANDOFF.md with Sequence 17, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 016. State SUCCESS/PARTIAL/FAILED, old/new image IDs, deployed SHA, DB verification and fresh backup location, web/MCP checks, manual-only template evidence, downtime, anomalies, and whether rollback was used. Sanitize secrets. Push only handoff file to experiment/ai-handoff, no force; no source changes or further tasks.

## Explicit boundaries
No coordinate conversion, hardware LED commands, Home Assistant changes, unrelated services or credentials, or additional production changes. Stop after reporting.
