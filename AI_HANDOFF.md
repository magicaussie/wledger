# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 42
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 028 — Controlled Dashboard Production Deployment
Production-Authorization: EXPLICIT_USER_APPROVAL_GRANTED
Approved-Production-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Previous-Production-Commit: 7b5f63a11747310752aa2a186964d1970d36585f

## Authorization and workflow
User explicitly approved deployment and instructed ChatGPT to continue routine safe work autonomously, asking only if genuinely uncertain. This authorizes controlled WLEDger dashboard deployment, pre-deploy backup, necessary WLEDger app/MCP recreation, health verification and rollback on failure. It does NOT authorize operating physical LEDs, changing Home Assistant, modifying unrelated services, changing bin mappings or manual production DB changes. Do not request routine confirmation.

## Source-reviewed change
Task 026 merged into main at 696475c, parent 7b5f63a. Changed only dashboard SQL queries, generated sqlc code, dashboard Go service/tests, grid/wall templ components and generated outputs, render tests. No schema migration. New controller-driven LEFT JOINs preserve empty controllers/containers; service guards nullable IDs. Bounded tile text keeps title and aria-label. DeepSeek reports sqlc/templ deterministic, build/vet/full tests/race clean; ChatGPT independently inspected source.

## Production baseline
Host mainserver 192.168.1.108; app https://storage.localdomain/ via nginx-proxy-manager -> port 8090; MCP 127.0.0.1:9100 only. Current isolated release /home/spetchal/wledger-release-7b5f63a; original dirty checkout /home/spetchal/wledger MUST remain untouched. Compose project wledger with --env-file /home/spetchal/wledger/.env. Persistent data/logs/uploads are symlinked to original shared dirs. Prior successful backup /home/spetchal/backups/wledger-pre025-20261010T063927Z. SQLite goose 10, led_coordinate_space=drawer, 1 controller, 2 containers, 68 mapped bins, 2 parts, 2 assignments, 15 audit logs, 1 user. Expected physical mapping digest 1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09. Digest method: SHA-256 of semicolon-joined rows id:container_id:led_index:width ordered by bin id (verify against baseline before proceeding).

## Implementation-ready deployment procedure
1. Read AGENTS.md and previous Sequence 37 deployment report. SSH mainserver and verify running production commit 7b5f63a, origin/main 696475c, healthy containers, no unexpected local changes in release, no production configuration drift, no SQL migrations in diff. Check free disk space, image tags and rollback assets. Never display secrets.
2. Create new pre028 backup in /home/spetchal/backups/wledger-pre028-<UTC timestamp>, root-owned mode 700. Reuse Task 025 proven SQLite online-backup API procedure (not raw copy of active DB); back up production compose/config/.env with strict permissions, uploads, source provenance and running app/MCP images by immutable ID. Produce MANIFEST.sha256, verify sha256sum -c. Independently verify backup DB integrity_check=ok, foreign_key_check empty, goose10, drawer coordinates, counts, and bin mapping digest equal live baseline. Stop on mismatch.
3. Create isolated detached-HEAD checkout /home/spetchal/wledger-release-696475c pinned to approved commit; never reset/clean/pull original production checkout. Symlink data/logs/uploads to shared persistent directories exactly as in previous release. Verify compose and Dockerfile unchanged from 7b5f63a. Build and recreate only WLEDger project services, using proven command form:
   docker compose -p wledger --env-file /home/spetchal/wledger/.env -f docker-compose.yaml build
   docker compose -p wledger --env-file /home/spetchal/wledger/.env -f docker-compose.yaml up -d
   Run these from isolated release directory. Preserve old release, image IDs and rollback tags.
4. Verify both containers running, RestartCount=0, startup logs clean (expected goose no migrations), HTTPS /login returns 200, protected routes redirect/deny as expected, authenticated API and loopback MCP work without exposing token, MCP external access denied. Confirm running container compose labels point to new release. Compare live DB integrity, FK, goose, coordinate space, controller/container/bin counts and digest before vs after. Confirm no accidental WLED requests, physical LED actions or DB modifications.
5. Dashboard visual validation: do not log in with user credentials or fabricate success. If a safe local authenticated dev browser exists, check long names, empty controller state, legacy grid and wall modal at desktop/mobile widths. Otherwise report visual check PENDING USER; do not block healthy deployment solely because visual check is pending. No physical LED operations. The previous Task 025 Secure cookie/CSRF browser checks also remain pending.
6. If deployment fails, roll back to previous release/image using preserved assets, recheck health and mapping digest, and report. No destructive DB restore without separate approval. If unexpected production topology or mapping change occurs, STOP and report.
7. Publish Sequence 43 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 028 with actual release path, pre028 backup, image IDs, verification results, before/after DB invariants and digest, production commit, rollback status, and pending browser checks. Stop.

## Guardrails
No production migration/schema change, manual DB edits, bin/LED mapping changes, physical WLED commands, Home Assistant changes, unrelated container/proxy changes, secret disclosure or original dirty checkout edits. Authorization is scoped to this dashboard release only.
