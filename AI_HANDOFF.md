# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 70
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 041 — Controlled Production Deployment of Main 5acb006
Production-Authorization: USER_EXPLICITLY_AUTHORIZED_THIS_DEPLOYMENT
Expected-Main: 5acb00678da633a40dc6a4e6ac17b60275c40b91
Expected-Running-Release: a12d824e48da7c19b8ad508027898492b9d84c81
Scope: WLEDger app and MCP service ONLY

## Authorization and intent
User explicitly said 'update' in response to the offer to update the production Docker release. Authorization covers this one controlled production deployment, its prerequisite verified backup and necessary rollback if validation fails. It does NOT authorize LED commands, Wall creation, coordinate conversions, HA changes, other services, or modifying the original dirty checkout.

## Implementation-ready plan
1. Preflight read-only: confirm remote origin/main exactly 5acb006, main ancestry from a12d824, clean isolated source checkout, compose paths, actual running container image IDs, ports, volumes, Docker config and service health. Compare main delta a12d824..5acb006 and confirm only test harness/tests/.gitignore, package.json, CI YAML, and generated CSS; no migrations, SQL, auth, service wiring, compose, Dockerfile or LED logic. STOP if unexpected. Record existing LED coordinate space, controller/string/bin counts, mapped/unmapped, mapping digest and DB integrity/FK/goose version. Avoid hardware actions.
2. Take a NEW complete production backup before ANY build/recreate that might affect production. Follow verified Task035 procedure using a fresh timestamped root-protected backup directory under /home/spetchal/backups: SQLite online backup (not unsafe live file copy), .env/config, uploads, provenance, immutable image saves/rollback tags for BOTH app and MCP, SHA256 manifest, restore instructions. Verify manifest, DB integrity, foreign keys, schema migration version, mapping digest and restore prerequisites. Preserve previous complete backup /home/spetchal/backups/wledger-pre035-20261010T110002Z. Do not use incomplete ...T105604Z as rollback.
3. Prepare NEW isolated release checkout, e.g. /home/spetchal/wledger-release-5acb006, exact immutable SHA, no changes to /home/spetchal/wledger dirty checkout. Reuse verified release workflow/compose, existing external mounts, environment, HTTPS reverse proxy and MCP loopback restrictions. Build app and MCP from the exact commit, verify build outputs and Docker image IDs. Ensure Docker css-builder runs npm ci + minified Tailwind generation and that gap-x-4/gap-y-1 are present in generated runtime CSS. No DB migrations expected.
4. Before cutover verify backup and rollback images again. Recreate ONLY wledger and wledger-mcp services via the same approved deployment mechanism as Task035, keeping persistent DB/uploads/logs and service topology unchanged. No prune or broad docker compose down. Stop if deployment requires unrelated service restarts.
5. Validate both containers Up and restart counts stable; check app HTTPS /login 200 and / redirects, authenticated/unauthenticated protected routes using existing authorized test credentials only, MCP loopback 127.0.0.1:9100 behavior (unauthorized 401; authorized expected response) and external exposure blocked. Check logs, DB integrity/FK/goose10, counts, coordinate space, mapping digest EXACTLY equal to baseline. No LED/Locate/Global-Off POST. Check production CSS generated and Wall page routes read-only; zero Walls is expected if still none configured. No physical hardware commands.
6. On any failed invariant, execute verified rollback to previous immutable app and MCP images using the NEW backup instructions, avoid DB restore unless truly needed, and recheck mappings/health. STOP and report any mismatch; do not improvise database edits.
7. Publish Sequence 71 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task041 with precise backup path, verification manifest, release path, old/new image IDs, deployed SHA, services restarted, health/HTTP/auth/MCP/CSS results, before/after mapping digest and counts, logs, rollback readiness, and any limitations. STOP.

## Existing verified reference
Task035 deployed a12d824 in isolated release /home/spetchal/wledger-release-a12d824. Complete pre035 backup /home/spetchal/backups/wledger-pre035-20261010T110002Z, mapping digest 1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09, coordinate space drawer, 1 controller, 2 containers, 68 bins, 68 mapped, 0 unmapped, goose10, 0 dashboard Walls. Old rollback images were tagged at pre035; DO NOT assume current image IDs until inspected.

## Guardrails
Do not touch other Docker services, original dirty checkout, HA, real WLED LEDs or mapping coordinates. No new Wall or test data in live DB. Do not expose credentials in handoff. User authorization consumed after this deployment; any later production update requires new permission.
