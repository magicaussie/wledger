# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 11
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 013 — Stage B Controlled Production Upgrade
Production-Authorization: STAGE_B_UPGRADE_ONLY (consumed)
Target-Commit: 63016f4e75134ae0675934818fb77f53dd463800
Result: SUCCESS

## Outcome
Stage B completed successfully. Production mainserver now runs the approved commit `63016f4`. Migration 010 and the expected drawer-allocation backfill ran; there were no LED index changes and no manual coordinate conversion. No rollback was needed.

## Preflight (all passed)
- origin/main == target `63016f4`; AGENTS.md confirmed.
- Production `mainserver`: containers up; pre-upgrade images `wledger` = `sha256:25560ce6…`, `wledger-mcp` = `sha256:e2582152…`.
- Pre-upgrade DB: goose 9; system_flags `{migration_005_applied=true}` only; no `led_start`/`led_count`; counts 1/2/68/2/2/14; 68 mapped bins; bins digest `1b0f9bd7…`.
- Stage A backup re-verified: dir present, `sha256sum -c MANIFEST.sha256` all OK, snapshot integrity ok / FK clean, both image tars readable; free space 1022 GiB.

## Deployment method (clean isolated release)
- New clean clone at `/home/spetchal/wledger-release-63016f4`, `git checkout 63016f4…` (HEAD verified).
- `data`/`uploads`/`logs` in the release dir are symlinks to the existing production dirs `/home/spetchal/wledger/{data,uploads,logs}` (bind-mount resolution confirmed before deploy). The dirty production checkout was NOT reset/cleaned/pulled.
- Built images from the clean tree; deployed with `docker compose -p wledger --env-file /home/spetchal/wledger/.env -f docker-compose.yaml up -d` (project name + container names preserved). Token supplied via env file and never printed.

## Images (old → new)
- wledger: `sha256:25560ce6a…` → `sha256:08f01bb8df611bab93a3bc798d56a69813da64dba1f4c6a4ad71ec4c23913d77`
- wledger-mcp: `sha256:e2582152…` → `sha256:70e31ce080106ca13a125053fb085fc6d03c3782b863ce27622f48cb926bfffc`
- Old images preserved: `wledger-wledger:rollback-20261010T040208Z` (`25560ce6…`), `wledger-mcp-server:rollback-20261010T040208Z` (`e2582152…`) plus Stage A tar archives.

## Migration & startup (from container logs)
- `OK 010_drawer_led_allocation.sql` → `goose: successfully migrated database to version: 10`.
- `MigrateLegacyLedIndices` completed (gated no-op: space=segment, `migration_005_applied` already true — no bin changes).
- `drawer LED allocation backfill complete` clean=2, empty=0, inconsistent=0.
- `Server listening` on :8080.

## Post-upgrade database verification (live, read-only)
- integrity_check: ok; foreign_key_check: CLEAN; goose version: 10.
- counts unchanged: controllers 1, drawers 2, bins 68, parts 2, part_assignments 2, audit_logs 14; bins mapped 68 / unmapped 0.
- **Bin LED mapping digest unchanged (`1b0f9bd7…` identical to preflight) → no LED index/coordinate changes.**
- system_flags now `{drawer_allocation_backfilled=true, migration_005_applied=true}`; no `led_coordinate_space` flag (still segment).
- Drawer allocations (expected): container 3 (seg 0) → led_start 0, led_count 628; container 4 (seg 1) → led_start 0, led_count 513.

## Web / API / MCP checks
- HTTPS web UI via reverse proxy: `https://storage.localdomain/` → 303 → `/login` (login page 200). Direct `:8090` → 303 → `/login`.
- MCP publication: `127.0.0.1:9100` only (loopback); external `192.168.1.108:9100` now CLOSED; `MCP_HTTP_ADDR=:9100` inside container.
- MCP auth: unauth GET/POST → 401; wrong bearer → 401; authenticated GET/POST (initialize) → 200. Tokens never printed.
- API: `https://storage.localdomain/api/v1/health` → 401 without token (expected).

## Downtime
- Short planned interruption only during container recreation (~2–3 s). Both containers resumed; no extended outage.

## Exact actions performed
1. Preflight read-only checks (origin/main, containers, images, DB, Stage A backup).
2. Created clean release `wledger-release-63016f4`; checked out and verified `63016f4`; symlinked data/uploads/logs to production; verified bind resolution.
3. `docker compose -p wledger build` (clean tree) → built both images.
4. `docker compose -p wledger --env-file …/.env up -d` → recreated `wledger` and `wledger-mcp`.
5. Verified logs, DB (integrity/FK/goose/counts/bin digest/allocations/flags), web via proxy, MCP auth + loopback publication, external port closure.
- No LED commands, no coordinate conversion, no secrets/hardware/HA/proxy/firewall changes.

## Notes / risks
- Authorized consequence: external (non-loopback) MCP clients, e.g. Home Assistant at 192.168.1.103, can no longer reach `:9100` and must be updated to send `Authorization: Bearer <WLEDGER_API_TOKEN>` behind an HTTPS proxy or loopback if required. Explicitly acceptable per authorization.
- The pre-existing production checkout `/home/spetchal/wledger` is untouched (HEAD `d7b5690…`, dirty tree preserved); a harmless warning about a root-owned `.git/index` was observed there (pre-existing, not caused by this task).
- Uploads were not re-synced; unchanged since Stage A.

## Next gate
No automatic Stage C/D beyond the non-mutating checks above. Any LED coordinate conversion or further production change requires separate explicit authorization.
