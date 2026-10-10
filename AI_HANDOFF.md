# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 13
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 014 — Stage C Post-Upgrade Functional Verification
Production-Authorization: READ_ONLY_VERIFICATION_ONLY (consumed)
Target-Commit: 63016f4e75134ae0675934818fb77f53dd463800
Result: PASS (with service/session limits noted)

## Method
Read-only production checks + committed-source review at `63016f4`. No writes, no form submissions, no LED commands, no WLED requests. Authenticated browser pages could not be exercised without a session; per instructions no session was manufactured or bypassed, so those are NOT TESTED and were covered by source review instead.

## 1. Production state (PASS)
- Containers: `wledger` and `wledger-mcp` Up, RestartCount 0, started 2026-10-10T04:09:46Z.
- Deployed release: `/home/spetchal/wledger-release-63016f4` HEAD = `63016f4…`.
- DB (live, read-only): integrity ok, foreign_key_check CLEAN, goose version 10.
- Counts: controllers 1, drawers 2, bins 68, parts 2, part_assignments 2, audit_logs 14; bins mapped 68 / unmapped 0.
- LED digest unchanged vs pre-upgrade: `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09`.
- Allocations: container 3 (seg 0) start 0 / count 628; container 4 (seg 1) start 0 / count 513.
- Flags: `{drawer_allocation_backfilled=true, migration_005_applied=true}`; no `led_coordinate_space` flag ⇒ segment-relative coordinate space.

## 2. Source review at 63016f4 (informational)
- Coordinate-space writers (non-test): only `internal/backup/service.go` (restore sets it from a manifest) and `internal/hardware/conversion.go` (explicit conversion). Grid editing, drawer/bin views and imports never set it.
- Conversion guards (`internal/handler/conversion.go`): `GET /hardware/conversion` = read-only preview, admin-only; `POST /hardware/conversion` requires admin + CSRF + `confirm=confirm` + a non-empty preview fingerprint, and the engine re-validates in a write transaction (all-or-nothing). Not triggerable accidentally or by GET.
- Grid save (`internal/hardware/service.go` `SaveGridInSpace`): reads the active space inside the write transaction, refuses a save whose payload was authored in another space, and refuses when the space is unresolved. It never changes the stored space.
- Drawer view (`internal/handler/drawers.go`, `web/pages/drawer.templ`): read-only; renders each bin's segment-relative `LED {led_index}` badge and its contents. Note: the page issues `POST /drawers/{id}/locate` on load (`hx-trigger="load"`) and via a Locate button, so viewing a drawer flashes its LEDs (intended "scan → highlight" behaviour).
- Locate endpoints (`/hardware/{id}/locate`, `/parts/{id}/locate`, `/drawers/{id}/locate`) are POST-only, so they are not reachable by any GET I issued.

## 3. Web route checks (read-only, unauthenticated) — PASS
Direct (`http://192.168.1.108:8090`) and via proxy (`https://storage.localdomain`):
- `/login` → 200.
- `/`, `/scan`, `/parts`, `/parts/9`, `/drawers/3`, `/hardware`, `/hardware/3/status`, `/hardware/3/grid`, `/hardware/conversion`, `/settings`, `/bin/69/qr`, `/drawer/3/qr`, `/cabinet/3/qr` → 303 → `/login` (auth guard active; no data leaked).
- Authenticated in-browser rendering of dashboard/cabinet/drawer/bin/inventory pages: **NOT TESTED** (no safe session available; not fabricated).

## 4. Authorized read-only API checks — PASS
- `GET /api/v1/health` without token → 401; with token → 200 `{"status":"ok"}`.
- `GET /api/v1/hardware` (auth) → 200; `GET /api/v1/parts?q=a` (auth) → 200 (read-only).
- MCP `initialize` (auth) → 200; no `tools/call` invoked (no mutation, no LED). Tokens never displayed.

## 5. Coordinate-space consistency — PASS
Segment-relative space is preserved: DB has no `led_coordinate_space` flag, drawer allocations are present, and the digest proves bin indices are byte-identical to pre-upgrade. The drawer template presents segment-relative LED indices consistently. No UI path was found that could accidentally trigger a coordinate conversion (the only trigger is the guarded admin POST).

## 6. Logs / uptime / backup
- `wledger` log: 0 ERROR; 2 WARN, both `denied read access` from the unauthenticated probes (expected).
- Restarts: 0 for both containers.
- Stage A backup re-verified intact: `sha256sum -c MANIFEST.sha256` all OK.

## Evidence table
| Check | Result | Evidence |
| --- | --- | --- |
| Deployed commit = 63016f4 | PASS | release HEAD |
| Containers up, restarts 0 | PASS | docker ps / inspect |
| DB integrity + FK | PASS | integrity ok / CLEAN |
| goose version 10 | PASS | goose_db_version |
| Row counts unchanged | PASS | 1/2/68/2/2/14 |
| 68 mapped bins, digest identical | PASS | bins digest `1b0f9bd7…` |
| Drawer allocations as expected | PASS | (3:0,628)(4:0,513) |
| Space still segment | PASS | no flag; source review |
| `/login` 200 | PASS | HTTP |
| Auth routes gate to /login | PASS | 13 routes → 303 |
| API health/auth | PASS | 401 unauth / 200 auth |
| MCP auth non-mutating | PASS | initialize 200, no tools/call |
| No accidental conversion path | PASS | source review |
| Authenticated UI pages | NOT TESTED | no safe session |
| Runtime errors since upgrade | PASS (0 ERROR) | docker logs |

## Risks / notes
- Viewing a drawer (authenticated) auto-issues an LED locate on load — expected, but it is an LED action caused by a page view.
- External MCP / Home Assistant connectivity is out of scope and unchanged since Stage B.
- No failures attributable to the upgrade were observed.

## Smallest safe next step
If authenticated UI validation is desired, have an authorized admin log in and page through dashboard / cabinet(grid) / drawer / bin / inventory; no automated session creation is proposed. No further production change is required. Any Stage D (LED coordinate conversion) remains out of scope and needs separate explicit authorization.
