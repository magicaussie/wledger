# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 37
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 025 — Controlled Production Deployment of Security Fixes
Production-Authorization: EXPLICIT_USER_APPROVAL_GRANTED (consumed)
Deployed-Production-Commit: 7b5f63a11747310752aa2a186964d1970d36585f
Previous-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Result: SUCCESS — deployed and verified (no rollback)

## Preflight (all passed)
- Host `mainserver` (192.168.1.108) reached over SSH; GitHub `origin` `refs/heads/main` == `7b5f63a11747310752aa2a186964d1970d36585f`.
- Running production before deploy: isolated release `/home/spetchal/wledger-release-ca2789f5` (git HEAD `ca2789f5`), images `wledger-wledger` = `sha256:886f3f36…`, `wledger-mcp-server` = `sha256:70e31ce0…`; compose project `wledger`; RestartCount 0; both `Up`.
- Deploy range `ca2789f5..7b5f63a` = exactly 11 files (`cmd/server/main.go`, `internal/config/security.go`, `internal/config/security_test.go`, `internal/csrf/csrf.go`, `internal/middleware/csrf.go`, `internal/router/router.go`, `internal/router/router_auth_test.go`, `web/layouts/base.templ`, `web/layouts/base_templ.go`, `web/static/js/csrf.js`, `docs/environment.md`). **No `sql/` or migration files ⇒ no migration expected.**
- Production `.env` contains only `WLEDGER_API_TOKEN`; `WLEDGER_INSECURE_COOKIES` **absent** ⇒ Secure session-cookie default is in force. Running container env carries only `WLEDGER_API_TOKEN`, `WLEDGER_PUBLIC_URL` (and PATH).
- HTTPS for `storage.localdomain` is terminated by nginx-proxy-manager (openresty) proxy host `2.conf` (443 → `192.168.1.108:8090`, `X-Forwarded-Proto https`, websocket upgrade); a Secure cookie is therefore compatible. The proxy was read only, never modified.

## Pre-deploy backup (pre025)
- Path: `/home/spetchal/backups/wledger-pre025-20261010T063927Z` (root:root; dir mode 700, files mode 600; outside the repository).
- Method: SQLite **online backup API** from a `mode=ro` source connection (not a file copy) → `db/wledger.db`, proven **self-contained** (copying only that file reproduces integrity ok, FK clean, goose 10, 68 bins and the identical digest).
- Also captured: both running images saved by immutable ID + rollback tags; `uploads/uploads.tar.gz`; `config/` (compose, Dockerfile, .dockerignore, .gitignore, `.env` [secret, mode 600]); `source/` (HEAD, remotes, status, tracked diff, untracked diagnostics); `provenance.txt`; `RESTORE_NOTES.md`; `MANIFEST.sha256` → `sha256sum -c` **OK=17, FAILED=0**.
- Rollback tags added: `wledger-wledger:rollback-pre025-20261010T063927Z` (= `886f3f36…`), `wledger-mcp-server:rollback-pre025-20261010T063927Z` (= `70e31ce0…`).

## Deployment
- New clean isolated checkout pinned to `7b5f63a` at `/home/spetchal/wledger-release-7b5f63a` (detached HEAD verified). `data`/`logs`/`uploads` are symlinks to `/home/spetchal/wledger/{data,logs,uploads}`; the production `.env` is supplied via `--env-file`. The original dirty production checkout `/home/spetchal/wledger` was **not** reset, cleaned, pulled or edited (HEAD still `d7b5690…`; `git status --porcelain` identical to the pre-deploy snapshot).
- `docker-compose.yaml` and `Dockerfile` in the new release are **byte-identical** to the `ca2789f5` release ⇒ no service/port/env changes.
- Built both images: `docker compose -p wledger --env-file /home/spetchal/wledger/.env -f docker-compose.yaml build`.
- Deployed only the WLEDger project: `… up -d` → recreated **only** `wledger` and `wledger-mcp`. No unrelated service or proxy was touched. Brief `wledger` interruption (~1–2 s) during recreate.

## Images (old → new; running container image IDs)
- wledger: `sha256:886f3f36…` → `sha256:e2efa2fb60dadd79ec56bc8edc68dc9872e66ed6bf175d821212e4d5577927c8` (tag `wledger-wledger@sha256:e2efa2fb…`).
- wledger-mcp: `sha256:70e31ce0…` → `sha256:a88c59eaa0fc9de32ddd60125f442cf94a0784f738a798f6de15458a1c56c1ba` (tag `wledger-mcp-server@sha256:a88c59ea…`).

## Verification (post-deploy) — PASS
- Containers: `wledger` and `wledger-mcp` both `Up`, RestartCount **0**, state running; compose labels show `working_dir = /home/spetchal/wledger-release-7b5f63a`.
- Startup log: `goose: no migrations to run. current version: 10`; one expected WARN `skipping legacy LED index migration: bin LED indices are not segment-relative (space=drawer)`; `Server listening` (8080); **0 ERROR** lines for both `wledger` and `wledger-mcp`.
- Web: `https://storage.localdomain/` → 303 → `/login`; `/login` → 200 (`<title>WLEDger - Login</title>`); direct `http://192.168.1.108:8090/` → 303.
- API: `/api/v1/health` → 401 without token, 200 with token.
- MCP: bound to **loopback only** (`127.0.0.1:9100 → 9100`, no `0.0.0.0` listener); `POST /mcp` → 401 without token, 200 with token; external `192.168.1.108:9100` unreachable.
- Deployed CSRF asset: `/wledger/web/static/js/csrf.js` present with `sha256 babf8518…` equal to the release source; the base layout renders `<meta name="csrf-token">` for authenticated users and loads `csrf.js`; the router gates Locate/GlobalOff with `RequireRole("editor","admin")` + `RequireCSRF`.

## State invariants — PASS (pre == post)
- DB: `integrity_check` **ok**, `foreign_key_check` **CLEAN**, goose **10**.
- `led_coordinate_space = drawer`; other flags unchanged (`drawer_allocation_backfilled=true`, `migration_005_applied=true`).
- Counts identical: controllers 1, containers 2, bins 68, parts 2, part_assignments 2, audit_logs 15, users 1, sessions 23.
- Bins mapped **68** / unmapped 0.
- Bin LED mapping digest **`1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09`** — exactly equal to the established baseline (unchanged).
- Allocations unchanged: container 3 (controller 3, segment 0, `led_start 0`, `led_count 628`); container 4 (controller 3, segment 1, `led_start 0`, `led_count 513`).
- One controller / two strings preserved; original production checkout preserved.

## Rollback
- **Not used** — deployment and verification succeeded. Rollback assets remain available: the `pre025` backup + `rollback-pre025-*` image tags + saved image tars.

## Pending browser checks (NOT performed; require separate authorization)
- Authenticated browser session over `https://storage.localdomain/`; inspect the session `Set-Cookie` for `Secure`.
- From the UI, confirm all three Locate actions and Global Off succeed with a valid CSRF token.
- Confirm a `viewer` account is forbidden (403) from those actions.
- Note: `GET /login` sets **no** cookie (guests are left untouched by `CSRFContext`), so `Secure` could not be observed without credentials; this check remains pending.

## Boundaries respected
No physical LED/WLED commands, no Locate clicks, no Home Assistant change, no coordinate conversion, no secrets printed, no production DB edit or restore, and no other service or proxy touched.

## Evidence / SHAs
- Deployed production commit: `7b5f63a11747310752aa2a186964d1970d36585f`
- Previous production commit: `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`
- Release path: `/home/spetchal/wledger-release-7b5f63a`
- Backup: `/home/spetchal/backups/wledger-pre025-20261010T063927Z`
