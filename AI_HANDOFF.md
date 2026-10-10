# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 17
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 016 — Deploy Manual-Only Drawer Locate Fix
Production-Authorization: CONTROLLED_DEPLOYMENT_OF_APPROVED_FIX_ONLY (consumed)
Target-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Previous-Production-Commit: 63016f4e75134ae0675934818fb77f53dd463800
Result: SUCCESS

## Preflight (all passed)
- origin/main == target `ca2789f…`; the range `63016f4..ca2789f` is exactly one commit (PR #1) touching only `web/pages/drawer.templ`, `web/pages/drawer_templ.go`, `web/pages/drawer_render_test.go`, `internal/handler/drawers_test.go` — no schema/source changes elsewhere.
- Production: `wledger`/`wledger-mcp` Up, RestartCount 0; prior images `wledger-wledger` = `sha256:08f01bb8…`, `wledger-mcp-server` = `sha256:70e31ce0…`; env keys `WLEDGER_API_TOKEN`, `WLEDGER_PUBLIC_URL`; bind mounts resolve through the release dir symlinks to `/home/spetchal/wledger/{data,uploads,logs}`.
- Production DB: integrity ok, foreign_key_check CLEAN, goose 10, counts 1/2/68/2/2/14, 68 mapped/0 unmapped, digest `1b0f9bd7…`, allocations `(3:0,628)(4:0,513)`, flags `{drawer_allocation_backfilled, migration_005_applied}`. No schema file changed in the range ⇒ no migration expected.

## Backups / rollback assets (before replacing containers)
- Stage A backup re-verified: `sha256sum -c` OK=17, FAILED=0.
- Fresh WAL-consistent backup of the current goose-v10 DB created: `/home/spetchal/backups/wledger-pre016-20261010T043616Z` (root-owned, mode 700) containing `db/wledger.db` + `db/verification.txt` + `MANIFEST.sha256`; single file proven self-contained (integrity ok, FK clean, goose 10, counts match); manifest verifies OK.
- Immediate pre-deploy image preserved: `wledger-wledger:rollback-pre016-20261010T043616Z` = `sha256:08f01bb8…`.

## Deployment
- New clean release checkout pinned to `ca2789f` at `/home/spetchal/wledger-release-ca2789f5` with `data`/`uploads`/`logs` symlinks to the existing production dirs and the existing production `.env` (via `--env-file`). Original `/home/spetchal/wledger` checkout untouched.
- Built only the WLEDger service; recreated only `wledger` (`docker compose -p wledger … up -d --no-deps --no-build wledger`). MCP was not rebuilt or recreated.

## Images
- wledger: old `sha256:08f01bb8df611bab93a3bc798d56a69813da64dba1f4c6a4ad71ec4c23913d77` → new `sha256:886f3f36217fe9d1b5a08be7f31f6da24dd00bd2058f15b8a211a006321613b3`.
- wledger-mcp: unchanged `sha256:70e31ce080106ca13a125053fb085fc6d03c3782b863ce27622f48cb926bfffc` (not restarted).

## Verification
- Release HEAD `ca2789f…`; running wledger image `886f3f36…`; wledger restart count 0.
- Startup log: "goose: no migrations to run. current version: 10" ⇒ no migration ran. 0 ERROR log lines.
- DB unchanged: integrity ok, FK CLEAN, goose 10, counts 1/2/68/2/2/14, digest `1b0f9bd7…` (identical), allocations and flags unchanged.
- Web: `https://storage.localdomain/` → 303 → `/login`; `/login` → 200; direct `:8090` `/` → 303.
- API: `/api/v1/health` 401 without token, 200 with token. MCP `initialize` (auth) → 200 (MCP untouched).
- Manual-only evidence: release `web/pages/drawer.templ` has 0 `hx-trigger="load"` and 1 locate control; offline test `TestDrawerDetailRendersLocateManually` PASS at `ca2789f`, plus handler drawer tests PASS.

## Downtime / anomalies
- Brief interruption only while recreating the `wledger` container (~1–2 s). MCP stayed up.
- Anomaly (no production impact): the first fresh-backup attempt aborted under `set -e` because `grep -c` returned non-zero; re-run succeeded. The fresh backup also initially contained empty `-wal`/`-shm` artifacts and a self-referential manifest line; these were removed and the manifest regenerated after proving the single DB file is self-contained.
- Rollback NOT used (deployment and verification succeeded).

## Checks not performed (boundary-respecting)
- No authenticated drawer page opened and no Locate clicked (no LED/WLED commands sent). Authenticated in-browser UI: NOT TESTED (no safe session). No coordinate conversion, no HA changes, no other services/credentials touched.

## Next
- Await review. Any further production change (including LED coordinate conversion) requires separate explicit authorization.
