# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 22
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 019 — Fresh Pre-Conversion Database Backup
Production-Authorization: BACKUP_AND_READ_ONLY_CHECKS_ONLY
Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Related-Task: 018 (conversion remains pending; admin user will submit in UI)

## Context
The user is logged in as ADMIN in their own browser and has reviewed the existing conversion preview: 2 drawers, 2 convertible, 0 blocked, 68 bins affected, with all 68 segment-relative indices equal to the proposed drawer-relative indices. User authorized preparing the fresh backup before they submit. DO NOT submit conversion or use their session.

## Task
1. Read AGENTS.md and confirm current deployed release SHA, running containers, DB goose 10, coordinate-space still segment-relative (flag absent), integrity/FK clean, counts, allocations and digest. If already drawer-relative or any unexpected change, STOP and report.
2. Verify existing Stage A and pre-016 backup manifests.
3. Create a fresh, WAL-consistent, standalone SQLite online backup of the CURRENT goose-v10 production database using SQLite backup API, in a new root-owned mode-700 directory under /home/spetchal/backups outside the repository. Preserve prior backups; never copy the live DB file directly. Ensure manifest does not include itself, exclude transient empty WAL/SHM artifacts, validate SHA-256, integrity_check, foreign_key_check, goose version, mapping counts/digest and allocation values against production. Keep any secrets private.
4. Perform read-only offline preview verification immediately after backup: expected fingerprint 4477c3b92003ae86ff4422637718d40a21761e239b18016aeb5b4e00b90ac03c; 2 convertible, 0 blocked, 68 mapped bins, all indices unchanged, allocations (drawer 3 segment 0 start 0 count 628; drawer 4 segment 1 start 0 count 513). If preview differs, STOP and report NOT READY.
5. Report READY FOR ADMIN UI CONFIRMATION only if all checks pass. Do not restart services, deploy, change source/DB data, run migrations, send LED commands, or invoke conversion POST.
6. Publish Sequence 23, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 019. Include exact backup path, verification results, fingerprint, production state, and whether user can now safely submit existing authenticated conversion UI (noting UI may need refresh for a current fingerprint). Commit/push only AI_HANDOFF.md to experiment/ai-handoff, no force; STOP.

## Important
The earlier Task 018 conversion authorization remains unconsumed; actual conversion must be performed by the legitimate logged-in admin in their own browser. Do not attempt to obtain cookies, passwords or CSRF tokens. Do not authorize the user to submit until backup completion is independently reviewed.
