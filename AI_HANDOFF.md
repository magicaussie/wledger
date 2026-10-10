# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 5
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 010
Production-Authorization: NONE
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## Summary
Task 010 completed as an investigative, non-destructive pass. Production (mainserver, 192.168.1.108, `/home/spetchal/wledger`) is at `d7b5690`, 14 commits behind baseline `63016f4`. B1 is effectively benign (tracked diffs are formatting-only); B4 and B5 are verified; the deployment path is a clean fast-forward. No production change was made. One possible secret was found in an untracked production file (value withheld).

## Evidence

### B1 — production dirty checkout (VERIFIED, sanitized)
- `git diff -w` on production shows only the deletion of `docs/api-and-mcp.md` (that file exists again in the baseline). Every tracked `.go` edit (`cmd/mcp-server/main.go`, `cmd/mcp-server/tools.go`, `internal/api/api.go`, `internal/api/handlers.go`) is whitespace/gofmt-only — no functional difference. A clean deployment would lose no application functionality.
- Untracked on production: `cmd/alidtest/` + `alidtest` binary (throwaway Aliexpress diagnostic), `debug_algolia.py` (diagnostic), `scripts/aliexpress_helper.mjs` (byte-identical duplicate of the tracked `scripts/aliexpress/aliexpress_helper.mjs`, sha256 `9e53e2ea...`), `wledger` (host-built server binary), `uploads/` (user data). None of these exist upstream at `63016f4`.
- RISK / POSSIBLE SECRET: `debug_algolia.py` contains a hard-coded third-party Algolia API key (value withheld here). It must never be committed to any branch; handle under AGENTS.md "possible secrets" policy.

### B3 — MCP exposure (partly UNKNOWN)
- The running MCP is the OLD build (from `d7b5690`): listens on `:9100` (all interfaces), with NO inbound authentication. Env: `MCP_TRANSPORT=http`, `MCP_HTTP_ADDR=:9100`, token set.
- Baseline MCP (`63016f4`) adds inbound bearer + Origin enforcement (`cmd/mcp-server/auth.go`) and defaults `MCP_HTTP_ADDR=127.0.0.1:9100`; its compose binds `127.0.0.1:9100:9100` and adds `MCP_ALLOWED_ORIGINS`.
- No other container on production references wledger/9100; no established 9100 connections observed. Home Assistant is on a separate host (192.168.1.103) that is not key-accessible from here, so external client dependence on port 9100 is UNKNOWN.

### B4 — old binary reading a goose-v10 DB (VERIFIED, source)
- Migration 010 is additive (`led_start`/`led_count INTEGER NOT NULL DEFAULT 0`). Old (`d7b5690`) queries use explicit column lists (no `SELECT *`), so the new columns and the extra `drawer_allocation_backfilled` system flag are ignored on read; INSERTs omit the columns and take the defaults.

### B5 — goose-v10 DB with old migration set (VERIFIED, disposable synthetic test)
- A synthetic local DB was migrated to v10 (current set), then the OLD migration set (001–009) was run against it: `goose.Up -> err = nil`, "no migrations to run. current version: 10". No production DB was used or copied.

### B2 — backup procedure (DRAFT; not executed)
- Preserve running image: `docker tag wledger-wledger:latest wledger-wledger:rollback-<ts>` then `docker save`.
- WAL-consistent DB: use the SQLite online-backup API (python3 `sqlite3.Connection.backup`, source opened `mode=ro`); or stop writers and copy `wledger.db` + `wledger.db-wal` + `wledger.db-shm` together. Never copy `wledger.db` alone.
- Also archive `uploads/`, `docker-compose.yaml`, `.env` (secret), `logs/app.log`.
- Verify: `integrity_check`=ok, `foreign_key_check` clean, counts (1 controller / 2 drawers / 68 bins / 2 parts / 14 audit), goose=9, recorded sha256. Store outside the live dir, e.g. `/home/spetchal/backups/wledger-<ts>/`.

## Commit SHAs inspected
- Approved baseline: `63016f4e75134ae0675934818fb77f53dd463800`
- Production HEAD: `d7b5690ca9debd37218468a63c215b5b21fc6c03`
- Prior handoff tip: `e9c155448845dd0cb7e35dd7e2e730b9a23f0109`
- Library: pressly/goose v3.26.0 (module cache; no repo SHA)

## Recommendation
1. Treat B1 as benign: deploy from a CLEAN `63016f4` checkout (do not `git pull` over the dirty tree). Preserve diagnostics outside the repo; never commit the secret-bearing file.
2. Preserve MCP connectivity: keep the current `9100:9100` publication unless external clients are confirmed absent. Any external MCP client must be updated to send `WLEDGER_API_TOKEN`, because the baseline MCP enforces inbound auth.
3. Fast rollback is viable (old binary runs on a v10 DB — B5 verified); the safest rollback remains restoring the pre-upgrade DB backup.

## Authorization required / smallest next gate
- Every production write needs explicit authorization (backup, image tag/save, stop writers, deploy, restore).
- Smallest next gate: authorize STAGE A only — create a WAL-consistent DB backup + image tag/save + source/config archive. This writes only to a new backup directory: no restart, no deploy, no migration.

## Risks / unknowns
- UNKNOWN: external MCP client dependence on port 9100 (Home Assistant config not readable from here).
- Possible secret in untracked production `debug_algolia.py` (value withheld).
- B4 is verified by source inspection only; an optional end-to-end rollback rehearsal is still advised before executing any rollback.

## Next Step
Await ChatGPT review; await authorization for STAGE A.
