# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 36
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 025 — Controlled Production Deployment of Security Fixes
Production-Authorization: EXPLICIT_USER_APPROVAL_GRANTED
Approved-Production-Commit: 7b5f63a11747310752aa2a186964d1970d36585f
Previous-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55

## Authorization
User explicitly approved deployment of commit 7b5f63a to production WLEDger at https://storage.localdomain/ on 2026-10-10. Authorization covers a controlled release, necessary pre-deploy backup, service replacement/restart, non-destructive health checks and rollback if needed. Do not infer permission for LED commands, modifying Home Assistant, or manually converting inventory coordinates.

## Existing production state
Host mainserver (192.168.1.108). Existing isolated release /home/spetchal/wledger-release-ca2789f5; original dirty production checkout /home/spetchal/wledger MUST remain untouched. Compose project wledger uses docker compose -p wledger --env-file /home/spetchal/wledger/.env -f docker-compose.yaml up -d from isolated release. App https://storage.localdomain/; MCP loopback 127.0.0.1:9100. SQLite goose v10, led_coordinate_space=drawer, one controller with two strings, 68 mapped bins, physical mapping verified. Earlier backups under /home/spetchal/backups. Preserve all state.

## Steps
1. Verify GitHub origin/main pinned to 7b5f63a, current production image/release commit ca2789f5, compose configuration and service health. Read AGENTS.md and production runbook. Confirm production .env does NOT set WLEDGER_INSECURE_COOKIES truthy; never print secrets. Check how the existing reverse proxy serves HTTPS, and verify cookie Secure default is compatible.
2. Take new pre025 backup using safe SQLite online backup API plus relevant persistent files/config, preserve owner/mode and verified SHA-256 manifest. Check SQLite integrity, foreign keys, goose version, coordinate space, controller/string/bin counts, and baseline bin LED physical-address digest. Ensure rollback assets are available. STOP if any unexpected discrepancy.
3. Create new isolated immutable release directory pinned to 7b5f63a. Preserve production checkout untouched; link persistent data/uploads/logs using existing proven deployment procedure. Build images, inspect compose diff, and deploy only WLEDger app/MCP containers (do not modify unrelated services or proxy). No migration is expected; verify none are introduced. Keep previous release and image for rollback.
4. Check container health, HTTPS login page and authenticated session viability (without requesting credentials or bypassing auth), MCP loopback and token-gating, service logs, and no unexpected restart loops. Confirm Set-Cookie Secure where feasible without user credentials; otherwise mark browser authentication check pending user. Verify DB integrity, FK, goose10, led_coordinate_space=drawer, 68 mappings, unchanged mapping digest and hardware inventory counts. Compare before/after.
5. On failure, revert to prior isolated release ca2789f5 and verify health, state and mappings; report exactly what happened. Do not perform destructive restores without further user approval. No physical LED/WLED commands.
6. Publish Sequence 37 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 025 with deployment result, release path, image digests, backups and verification, state invariants, any rollback, pending browser checks, and exact production commit. STOP.

## Guardrails
Do not touch original dirty checkout, manually edit SQLite, modify LED mappings, perform conversion, invoke physical WLED commands, modify Home Assistant, expose secrets, or alter other services. If the production setup differs materially from known state, STOP and report. Keep only essential runtime changes authorized for this release.
