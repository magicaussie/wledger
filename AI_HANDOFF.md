# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 58
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 035 — Authorized Controlled Production Deployment
Production-Authorization: EXPLICIT_USER_APPROVAL_2026-10-10
Approved-Target: a12d824e48da7c19b8ad508027898492b9d84c81
Expected-Current-Production: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Approval-Scope: WLEDger app and MCP containers only; UI+CI release

## Authorization and independent review
User explicitly replied "go" to ChatGPT's question "Would you like me to authorize DeepSeek to perform that controlled deployment now?" following Task 034 read-only readiness assessment. This authorizes a controlled production deployment of exact target a12d824, including a fresh verified backup, app/MCP container build and recreate, health checks, and rollback on failure. It does NOT authorize LED operations, wall creation, unrelated services, Home Assistant changes, destructive DB restore, schema manipulation, or edits to original dirty checkout. Sequence 57 reports current production 696475c, healthy, goose10, mapping digest baseline intact, sufficient disk and prior rollback artifacts.

## Execution plan (implementation-ready; adapt to verified production layout)
1. PRECHECK / STOP ON DRIFT: SSH directly to spetchal@192.168.1.108 (mainserver). Verify target origin/main EXACT a12d824e48da7c19b8ad508027898492b9d84c81; current isolated production release /home/spetchal/wledger-release-696475c HEAD EXACT 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb; app/MCP containers healthy and running from that release; compose config/mounts, env-file, image IDs, current DB integrity/FK/goose10/counts and mapping digest. STOP and report if any mismatch or unexpectedly running migration.
2. BACKUP BEFORE TOUCHING CONTAINERS: Reconstruct and execute the *verified actual* Task 028 online SQLite backup procedure (not the placeholder /path/to/predeploy_backup.sh). Create new root-owned mode700 backup /home/spetchal/backups/wledger-pre035-<UTC timestamp> containing SQLite consistent snapshot via SQLite backup API, app uploads/config/env snapshot (secret-safe), source/compose provenance, immutable current image IDs and rollback tags, restore notes and SHA256 manifest. Verify sha256sum -c, SQLite integrity_check, foreign_key_check, goose10, expected counts and digest BEFORE proceeding. No live DB file cp as a substitute for SQLite online backup. Do not print secrets.
3. ISOLATED RELEASE: git clone/checkout target into /home/spetchal/wledger-release-a12d824 (or verify clean existing release). DO NOT alter /home/spetchal/wledger dirty checkout. Inspect actual OLD docker-compose.yaml, volume mount source paths and env_file and match in NEW; use exactly the same live data/uploads/logs mounts and permissions, not assumed symlink names. Confirm no Docker/compose/schema/migration differences from old. STOP on differences or inability to identify mounts. Record immutable current image IDs and verified rollback tags. No volume deletion.
4. BUILD + RECREATE: Build pinned exact-target WLEDger app and MCP images from isolated release. Check build success before replacing running containers. Run docker compose -p wledger --env-file /home/spetchal/wledger/.env -f <verified-new-release>/docker-compose.yaml up -d (or the equivalent verified Task028 command) only for WLEDger app and MCP; ensure no unrelated project services affected. Never run down -v, prune, DB reset, or restore.
5. POSTDEPLOY: Confirm app/MCP images differ as expected, running, RestartCount 0, no unexpected migration (goose remains 10), no ERROR logs. HTTPS https://storage.localdomain/login ->200; protected API unauthenticated ->401 and authorized with existing valid token ->200 if available WITHOUT exposing token; MCP loopback 127.0.0.1:9100 ->401 unauth and ->200 authorized; external 9100 inaccessible. Check SQLite integrity/FK, counts baseline controllers1 containers2 bins68 mapped68 parts2 assignments2 audit15 users1 (allow legitimate concurrent changes only after explicitly investigating), led_coordinate_space=drawer and mapping digest EXACT 1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09. Do not trigger Locate, Global Off or any physical WLED calls. Preserve all production data.
6. ROLLBACK ON FAILURE: If critical health, DB invariant, mapping digest, unexpected migration or connectivity checks fail, STOP and rollback app/MCP images to pre035 immutable rollback tags using verified Task028 restore procedure, preserving live volumes. Recheck health and mapping. DO NOT restore SQLite DB snapshot or overwrite live data without fresh explicit approval; if data altered or rollback cannot be made safe, stop and report immediately. Report both deployment and rollback outcome, never conceal failure.
7. NON-HARDWARE BROWSER CHECK: If an authenticated browser/session is available without sharing credentials, verify dashboard view, modal opening/closing, Tab+Enter, tall grid scroll and mobile width. If no walls exist, do NOT create one; mark modal visual check pending and recommend staging. Do not click Locate/Global Off. Browser Secure-cookie/CSRF checks must not send live LED POST requests.
8. REPORT: Publish Sequence 59 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 035 with deployed SHA, backup path/permissions/manifest verification (no secrets), exact old/new image IDs, compose provenance, all pre/post checks, CI status, DB/mapping digest, restart counts, errors, any rollback, remaining visual checks. Commit/push ONLY AI_HANDOFF.md on experiment/ai-handoff. STOP.

## Safety gates
User authorized this one deployment only. No further automatic deployments. Stop rather than improvise when backup procedure, actual volume layout, source revision, DB invariant, compose scope, or rollback plan is uncertain. Do not change physical LEDs, Home Assistant, unrelated containers or the original dirty checkout.
