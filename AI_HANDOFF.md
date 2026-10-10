# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 8
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 012 — Stage A Production Backup
Production-Authorization: STAGE_A_BACKUP_ONLY
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## Explicit user authorization
The user approved Stage A only ("go") after being asked to authorize the production backup. The user also stated that temporary Home Assistant disruption is acceptable for a later upgrade; this does NOT authorize upgrading now.

## Objective
On production mainserver, create and verify a complete, recoverable pre-upgrade backup of the current WLEDger installation. Preserve running images and current data without restarting services or changing application/database state. Stop on errors and report clearly.

## Scope authorized
1. Read AGENTS.md and verify production target and live container/image/DB paths before any write.
2. Create a unique, root-protected timestamped backup directory OUTSIDE the live WLEDger directory, e.g. /home/spetchal/backups/wledger-<UTCstamp>, with restrictive permissions (umask 077). Do not overwrite previous backups.
3. Record exact running container IDs, image IDs, tags, compose/service configuration, Git HEAD and sanitized working-tree status. Preserve the **actual running images by immutable image ID**, not just whatever :latest currently points to. Use docker image save to durable tar archives; optional rollback tags may be added only to those verified image IDs. Verify archives exist and can be read. Avoid exposing environment secrets in logs/reports.
4. Use SQLite's online backup API for a consistent snapshot of the live WAL-mode DB (source read-only URI), to the backup directory. Do not checkpoint, edit, migrate, or stop production DB. Verify snapshot integrity_check, foreign_key_check, goose version, and relevant counts. Ensure backup connection is closed and backup file fully written.
5. Archive uploads and required local configuration and any production-only source/diagnostic files (including dirty checkout diff/status) with restrictive permissions. Preserve the source checkout version and enough config to recreate it. Include hidden files needed for deployment, but never display secret contents in handoff. Do not include unneeded transient binaries or duplicate huge data.
6. Write an inventory, SHA256 manifest, restore-oriented notes and verification outcomes. Protect backups containing secrets/user data. Ensure the backup is not accidentally published to Git.
7. Confirm filesystem free space and that application containers remain running. Report any discrepancy and stop; never proceed to an upgrade.
8. Perform read-only verification of the saved assets, hashes, SQLite consistency and archive integrity. Do not restore onto production or change service state. A disposable offline restoration check is permitted only if isolated and safe.

## Important cautions
- The earlier draft's expected counts (1 controller, 2 drawers, 68 bins, 2 parts, 14 audit) are historical reference values; verify actual current counts instead of treating them as immutable requirements.
- A backup of live uploads is not necessarily point-in-time synchronized with the DB; note any residual consistency risk.
- Do not include token values, credentials, .env content, user uploads, or sensitive diffs in AI_HANDOFF.md or the chat.
- Do not use a plain copy of wledger.db alone while WAL is active.
- Do not stop writers or containers, deploy new code, migrate DB, convert LED coordinates, change MCP ports/auth, rotate keys, alter Home Assistant, or run LED commands. All are outside authorization.
- If backup verification fails, preserve partial artifacts securely and report failure; do not declare success.

## Handoff response
After completion, fetch the current handoff branch and check concurrency. Replace this entire file with Sequence 9, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW. Include a concise sanitized report: backup directory, assets and sizes, image IDs, database verification and goose version, manifest status, risks, unchanged container status, exact actions performed, and any failures. Commit and push ONLY AI_HANDOFF.md to experiment/ai-handoff without force. Do not touch main.

## Next approval gate
Stage B upgrade requires a NEW, explicit user approval after ChatGPT reviews Stage A results.
