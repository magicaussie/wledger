# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 20
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 018 — Controlled Production LED Coordinate Conversion
Production-Authorization: ONE-TIME_EXPLICIT_CONVERSION_ONLY
Approved-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Approved-Preflight: Task 017, Sequence 19
Expected-Preflight-Fingerprint: 4477c3b92003ae86ff4422637718d40a21761e239b18016aeb5b4e00b90ac03c

## User authorization
The user explicitly authorized Task 018 to execute the controlled production conversion from segment-relative to drawer-relative LED coordinates, subject to all safeguards below. This is a ONE-TIME database operation only. No other production change, source change, deployment, LED command, Home Assistant change, or unrelated task is authorized.

## Pre-execution stop gates
1. Read AGENTS.md and verify deployed release/production commit == ca2789f54382bc2aa98b2d4535b09f9df1c21d55; inspect source for the actual approved admin conversion handler, transaction guards and fingerprint requirements. Verify containers healthy, DB goose v10, integrity_check OK, foreign_key_check clean, and coordinate-space currently segment-relative (flag absent). STOP on any mismatch.
2. Verify previous Stage A and pre-016 backups/manifests. Create a NEW fresh WAL-consistent SQLite backup immediately before conversion, in a root-owned restricted directory outside the repo, verify standalone integrity/FK/goose/counts and SHA manifest. Preserve all previous backups. Never copy a live SQLite main DB file as a substitute for SQLite online backup.
3. Recalculate a read-only conversion preview against current production data (prefer existing approved preview engine without mutation, or offline faithful computation), including fingerprint and each drawer's before/after mappings. Expected: 2 convertible, 0 blocked, 68 mapped/affected bins, 0 unmapped, no duplicates, no out-of-bounds or allocation overlap; drawer 3 (controller 3, segment 0, start 0, count 628, 36 bins), drawer 4 (controller 3, segment 1, start 0, count 513, 32 bins); all proposed indices exactly equal current indices; expected fingerprint above. STOP if any discrepancy, including fingerprint mismatch, unexpected pending migration, stale/ambiguous coordinate space, or any index change.
4. Verify API/HTTP access and a legitimate pre-existing authenticated ADMIN session or otherwise an approved supported non-HTTP invocation of the EXACT production conversion service with equivalent authorization/validation. Do NOT fabricate, bypass, extract or reset credentials, disable CSRF, weaken security controls, or introduce ad hoc SQL to flip the flag. If no safe authorized invocation is available, STOP and report BLOCKED, asking user to perform the UI confirmation. Do not make a speculative workaround.
5. Capture the pre-conversion bin mapping digest, flags, allocations, counts and audit count; ensure no concurrent app changes to mappings while executing. If concurrent changes cannot be safely excluded, rely on the engine's transactional fingerprint revalidation and stop on stale result.

## Authorized execution (only after all gates pass)
6. Execute the EXISTING, REVIEWED production conversion operation exactly ONCE using valid admin authentication, CSRF token, explicit `confirm=confirm` and the CURRENT verified fingerprint; alternatively use the existing supported production service path only if it enforces equivalent transactional validation. Do not issue direct SQL updates, run migrations, or call any hardware/WLED endpoint. Do not repeat after an ambiguous response; inspect read-only state first.
7. If execution reports an error, stale preview, or blocked drawer, STOP and report without attempting a workaround.

## Post-conversion verification
8. Read-only verification: coordinate-space flag now drawer; 68 bin index values and their mapping digest IDENTICAL to pre-conversion; 2 allocations unchanged; goose 10, integrity/FK clean, inventory/assignment counts unchanged, expected audit entry added exactly once, all mapped LED targets resolve to same (segment,index) as before. Check app health/logs and container restart counts. No physical LED test or authenticated drawer locate click.
9. Do not automatically restore DB on failure. Preserve evidence and backups, report precisely any partial or uncertain outcome. Conversion is expected to be atomic; if state is ambiguous, STOP.

## Handoff
10. Publish Sequence 21, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 018 with SUCCESS/BLOCKED/FAILED, evidence of all stop gates, method of authorized invocation, preview fingerprint, fresh backup path, pre/post digest and counts, coordinate-space flag, audit delta, any errors, and any unverified hardware assumptions. Sanitize secrets. Commit/push ONLY AI_HANDOFF.md to experiment/ai-handoff without force, then STOP.

## Explicit prohibitions
No WLED/LED commands, no hardware tests, no source commits, no deployments/restarts, no Home Assistant modifications, no direct database manipulation to force conversion, no bypass of auth/CSRF, no unapproved recovery, and no next task.
