# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 24
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 020 — Post-Conversion Production Verification
Production-Authorization: READ_ONLY_VERIFICATION_ONLY
Expected-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Pre-Conversion-Backup: /home/spetchal/backups/wledger-pre019-20261010T050510Z

## Context
The user personally performed the authenticated admin UI conversion. The UI displayed "Conversion complete. All bin indices are now drawer-relative", "Current coordinate space: Drawer-relative (D)", and 0 drawers/affected bins/convertible/blocked on the subsequent preview. This is a success indication, NOT independent DB verification.

## Task
1. Read AGENTS.md. Verify production release commit, containers, image IDs, uptime/restarts, startup/error logs and web/API/MCP health without mutation.
2. Open the current production SQLite database READ ONLY (no writes or migrations); verify integrity_check, foreign_key_check, goose 10 and `led_coordinate_space=drawer`. Verify other flags unchanged.
3. Verify counts: controllers 1, drawers 2, bins 68, parts 2, assignments 2; audit logs expected 15 (pre-conversion 14 plus exactly one conversion entry). Verify all 68 bins remain mapped, 0 unmapped, and LED mapping digest exactly `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09`. Verify allocations drawer 3 (controller 3, segment 0, start 0, count 628) and drawer 4 (controller 3, segment 1, start 0, count 513), unchanged and non-overlapping.
4. Compare against verified pre019 backup READ ONLY, including per-bin old/new LED indices, widths and resolved physical addresses `(segment_id, led_start + drawer_relative_index)`; all should match pre-conversion segment-relative targets `(segment_id, old_index)`. Check no duplicates, overlaps, out-of-range, unmapped or unexpected row changes. Do not infer from digest alone.
5. Confirm only expected conversion changes occurred (flag, audit entry, potentially rewrites to identical LED index values). If any discrepancy, STOP, preserve evidence and report without restoring or fixing.
6. Publish Sequence 25, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 020. Report PASS/FAIL, exact counts, digest, flags, audit entry, per-bin comparison, health checks, and any uncertainty. Commit/push ONLY AI_HANDOFF.md on experiment/ai-handoff without force. STOP.

## Boundaries
STRICTLY READ ONLY. No database changes, backups/restores, hardware/WLED commands, Locate button clicks, migrations, deployments, restarts, source edits, Home Assistant changes, or subsequent tasks.
