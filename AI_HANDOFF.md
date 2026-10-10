# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 43
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 028 — Controlled Dashboard Production Deployment
Production-Authorization: EXPLICIT_USER_APPROVAL_GRANTED (consumed)
Deployed-Production-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Previous-Production-Commit: 7b5f63a11747310752aa2a186964d1970d36585f
Result: SUCCESS — deployed and verified (no rollback)

## Preflight (all passed)
- Host `mainserver` (192.168.1.108) reached over SSH. Note: the `mainserver` hostname resolves to an offline Tailscale address (100.105.131.56, last seen 92d ago); the LAN IP `spetchal@192.168.1.108` was used and is the same host (`hostname` = mainserver).
- Running production before deploy: isolated release `/home/spetchal/wledger-release-7b5f63a` (HEAD `7b5f63a`); images `wledger-wledger` = `sha256:e2efa2fb…`, `wledger-mcp-server` = `sha256:a88c59ea…`; compose project `wledger`; RestartCount 0; both `Up`.
- Deploy range `7b5f63a..696475c` = exactly 10 files (dashboard SQL, generated sqlc, dashboard service/tests, grid/wall templ + generated, render tests). **No `sql/schema/` files ⇒ no migration expected.**
- `docker-compose.yaml` and `Dockerfile` in the new release are byte-identical to the `7b5f63a` release.
- Free disk: 1018G available (44% used). Rollback tags for pre025/pre016/20261010T040208Z present.
- Original dirty checkout `/home/spetchal/wledger` HEAD `d7b5690…`; status byte-identical to the pre-deploy capture (see below).

## Pre-deploy backup (pre028)
- Path: `/home/spetchal/backups/wledger-pre028-20261010T093605Z` (root:root; dir mode 700, files mode 600; outside the repository).
- Method: SQLite **online backup API** from a `mode=ro` source connection → `db/wledger.db`, proven **self-contained** (copying only that file reproduces integrity ok, FK clean, goose 10, 68 bins and the identical digest; the `-wal` file is 0 bytes).
- Also captured: both running images saved by immutable ID + rollback tags; `uploads/uploads.tar.gz`; `config/` (compose, Dockerfile, .dockerignore, .gitignore, `.env` [secret, mode 600]); `source/` (HEAD, remotes, status, tracked diff); `provenance.txt`; `RESTORE_NOTES.md`; `MANIFEST.sha256` → `sha256sum -c` **OK=18, FAILED=0**.
- Rollback tags added: `wledger-wledger:rollback-pre028-20261010T093605Z` (= `e2efa2fb…`), `wledger-mcp-server:rollback-pre028-20261010T093605Z` (= `a88c59ea…`).

## Deployment
- New clean isolated checkout pinned to `696475c` at `/home/spetchal/wledger-release-696475c` (detached HEAD verified). `data`/`logs`/`uploads` are symlinks to `/home/spetchal/wledger/{data,logs,uploads}`; the production `.env` is supplied via `--env-file`. The original dirty production checkout `/home/spetchal/wledger` was **not** reset, cleaned, pulled or edited (HEAD still `d7b5690…`; status identical to the pre-deploy snapshot).
- Built both images: `docker compose -p wledger --env-file /home/spetchal/wledger/.env -f docker-compose.yaml build`.
- Deployed only the WLEDger project: `… up -d` → recreated **only** `wledger` and `wledger-mcp`. No unrelated service or proxy was touched. Brief `wledger` interruption during recreate.

## Images (old → new; running container image IDs)
- wledger: `sha256:e2efa2fb…` → `sha256:5b9e68f24f47de09e70781475b70b6bec9e0b201489f2062ec4f1e21eb844325` (tag `wledger-wledger:latest`).
- wledger-mcp: `sha256:a88c59ea…` → `sha256:21407226a97b4edc3057334f6e1b8c3e3cab2e9e37a0ff2a80ba7f58e4aeff98` (tag `wledger-mcp-server:latest`).

## Verification (post-deploy) — PASS
- Containers: `wledger` and `wledger-mcp` both `Up`, RestartCount **0**, state running; compose labels show `working_dir = /home/spetchal/wledger-release-696475c`.
- Startup log: `goose: no migrations to run. current version: 10`; expected WARNs `skipping legacy LED index migration…` and `skipping drawer allocation backfill…` (space=drawer); `Server listening` (8080); MCP `listening on :9100`; **0 ERROR** lines for both.
- Web: `https://storage.localdomain/` → 303 → `/login`; `/login` → 200 (`<title>WLEDger - Login</title>`); direct `http://localhost:8090/` → 303.
- API: `/api/v1/health` → 401 without token, 200 with token.
- MCP: bound to **loopback only** (`127.0.0.1:9100`); `POST /mcp` → 401 without token, 200 with token; external `192.168.1.108:9100` unreachable.

## State invariants — PASS (pre == post)
- DB: `integrity_check` **ok**, `foreign_key_check` **CLEAN**, goose **10**.
- `led_coordinate_space = drawer`; other flags unchanged (`drawer_allocation_backfilled=true`, `migration_005_applied=true`).
- Counts identical: controllers 1, containers 2, bins 68, parts 2, part_assignments 2, audit_logs 15, users 1, sessions 23.
- Bins mapped **68** / unmapped 0.
- Bin LED mapping digest **`1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09`** — exactly equal to the established baseline (unchanged).
- Original production checkout preserved (HEAD `d7b5690…`; status byte-identical to pre-deploy capture).

## Rollback
- **Not used** — deployment and verification succeeded. Rollback assets remain available: the `pre028` backup + `rollback-pre028-*` image tags + saved image tars.

## Pending browser checks (NOT performed; require separate authorization)
- Authenticated dashboard visual validation (legacy controller grid, empty-controller state, wall modal, long names) at desktop/mobile widths — **PENDING USER**; no user credentials were used.
- Task 025 Secure cookie/CSRF browser checks also remain pending.

## Boundaries respected
No physical LED/WLED commands, no Locate clicks, no Home Assistant change, no coordinate conversion, no secrets printed, no production DB edit or restore, no bin/LED mapping change, and no other service or proxy touched.

## Evidence / SHAs
- Deployed production commit: `696475c8d58786f3e0e39c6b9e38e1a412f1f7cb`
- Previous production commit: `7b5f63a11747310752aa2a186964d1970d36585f`
- Release path: `/home/spetchal/wledger-release-696475c`
- Backup: `/home/spetchal/backups/wledger-pre028-20261010T093605Z`
