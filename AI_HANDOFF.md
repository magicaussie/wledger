# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 10
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 013 — Stage B Controlled Production Upgrade
Production-Authorization: STAGE_B_UPGRADE_ONLY
Target-Commit: 63016f4e75134ae0675934818fb77f53dd463800
Backup: /home/spetchal/backups/wledger-20261010T040208Z

## Explicit authorization
The user replied "go" to the specific request to authorize Stage B production upgrade. Temporary disruption of Home Assistant / external MCP connectivity is acceptable. This authorization does not extend to LED coordinate conversion, unrelated production modifications, credential rotation, or Stage D integrations.

## Required preflight (STOP if any fails)
1. Read AGENTS.md and verify current origin/main exactly equals the target commit, unless a new explicit approval is obtained. Verify production host/path, containers, image IDs, current goose version, counts, coordinate-space flags and controller/drawer/bin LED configuration. Reconcile differences against Stage A report.
2. Independently verify Stage A backup exists, is securely stored, `sha256sum -c MANIFEST.sha256` passes, DB snapshot integrity_check and foreign_key_check pass, and both Docker image archives are readable. Check free space. If backup invalid, STOP without deployment.
3. Preserve dirty production checkout and any untracked user files; do NOT git reset/clean/pull over them. Deploy from a NEW clean checkout/worktree or isolated release directory pinned to the approved commit. Ensure persistent bind mounts refer to the existing production data/uploads/logs, not empty directories. Ensure deployment uses the intended production .env securely.
4. Review current compose/service wiring and make a specific deploy/rollback plan. New compose publishes MCP only on host loopback and requires bearer authentication; temporary external client breakage is explicitly acceptable. Ensure MCP_HTTP_ADDR=:9100 inside container and that WLEDGER_API_TOKEN is set, without printing it.
5. Inspect startup migrations and any auto-backfill carefully. Migration 010 and its normal startup backfill are authorized as part of Stage B; manual LED coordinate conversion is NOT authorized. If preflight suggests destructive/unexpected data changes, STOP and report.

## Authorized execution
6. Deploy the exact approved target commit using a clean isolated release. Rebuild/recreate only WLEDger application and MCP containers as needed. A short planned interruption is authorized. Preserve production data/uploads/logs and backup assets. Never deploy a different commit.
7. Allow normal startup migration 010 and expected drawer allocation backfill, but do NOT invoke manual segment-to-drawer coordinate conversion or any physical LED commands.
8. Verify container startup/health and HTTP/API/MCP behavior using safe, non-mutating probes. Check new MCP auth rejects unauthenticated access and accepts authenticated access without revealing tokens; check loopback-only host publication. Check HTTPS web UI through existing reverse proxy.
9. Verify production DB goose version 10, integrity_check and foreign_key_check, inventory counts, bin LED mappings, segment/drawer allocation flags and geometry against preflight. Specifically confirm 68 mapped bins remain mapped, no unintended LED index/coordinate changes, and drawer allocation backfill is as expected. Avoid hardware LED commands.
10. If verification fails: STOP, preserve evidence, and do not perform speculative repair. Rollback to the exact preserved old images/config and pre-upgrade DB snapshot ONLY if the failure is clearly attributable to this upgrade and a safe rollback procedure has been verified. Otherwise report failure and request a new approval for any destructive restoration. Never overwrite newer user data without explicit approval.

## Safety and reporting
- Do not delete/reset production checkout or backups; do not alter unrelated services, Home Assistant, reverse proxy, credentials, or firewall.
- Do not run manual LED conversion or send LED commands.
- Do not expose secrets, private user data or sensitive logs in the handoff.
- If a step requires new scope or ambiguity arises, STOP and request authorization.
- On completion or stop, replace AI_HANDOFF.md with Sequence 11, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW. Report actual deployed commit, new/old image IDs, migration status, count and LED mapping checks, web/MCP checks, any downtime, risks and exact actions. State SUCCESS/PARTIAL/FAILED clearly.
- Fetch and verify branch tip for concurrency; commit ONLY AI_HANDOFF.md and push ONLY experiment/ai-handoff without force. Do not commit to main.

## Next gate
No automatic Stage C/D beyond the non-mutating post-deployment verification above. Any LED coordinate conversion or additional production change requires separate explicit authorization.
