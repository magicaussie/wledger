# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 59
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 035 — Authorized Controlled Production Deployment
Production-Authorization: EXPLICIT_USER_APPROVAL_2026-10-10 (consumed)
Deployed-Production-Commit: a12d824e48da7c19b8ad508027898492b9d84c81
Previous-Production-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Result: SUCCESS — deployed and verified (no rollback)

## Preflight (all passed)
- Host `mainserver` (192.168.1.108) reached over SSH (LAN IP; the `mainserver` hostname resolves to an offline Tailscale address).
- Remote `origin/main` == `a12d824` (target); release `/home/spetchal/wledger-release-696475c` HEAD == `696475c`.
- Containers `wledger`/`wledger-mcp` `Up`, RestartCount 0, running; images `5b9e68f2…`/`21407226…`; compose `working_dir` = `…/wledger-release-696475c`; `environment_file` = `/home/spetchal/wledger/.env`.
- Mounts: `wledger` binds `…/wledger-release-696475c/{data,logs,uploads}` (symlinks to `/home/spetchal/wledger/{data,logs,uploads}`); `wledger-mcp` uses anonymous volumes.
- Original dirty checkout HEAD `d7b5690…` (untouched). Disk 1018G free.
- DB precheck: integrity ok, FK clean, goose 10, counts 1/2/68/2/2/15/1, mapped 68/0, digest == baseline.

## Pre-deploy backup (pre035)
- **Complete, verified:** `/home/spetchal/backups/wledger-pre035-20261010T110002Z` (root:root, mode 700).
- Online SQLite backup API snapshot (mode=ro source) + images saved by immutable ID + rollback tags + uploads tar + config incl. `.env` (mode 600) + source provenance + `RESTORE_NOTES.md` + `MANIFEST.sha256` → `sha256sum -c` **OK=18, FAILED=0**.
- Snapshot DB: integrity ok, FK clean, goose 10, counts match, digest == baseline.
- Rollback tags: `wledger-wledger:rollback-pre035-20261010T110002Z` (= `5b9e68f2…`), `wledger-mcp-server:rollback-pre035-20261010T110002Z` (= `21407226…`).
- **NOTE:** an earlier **partial** backup dir `wledger-pre035-20261010T105604Z` exists (interrupted; contains only `provenance.txt` + the two image tars — no DB/config/manifest). It is incomplete and should be ignored/cleaned up separately. A matching `rollback-pre035-20261010T105604Z` tag also exists (same old image IDs).

## Deployment
- Isolated release `/home/spetchal/wledger-release-a12d824` (detached HEAD `a12d824`); `data`/`logs`/`uploads` symlinked to the shared dirs; `docker-compose.yaml` + `Dockerfile` byte-identical to the `696475c` release; changed files exactly the 14 expected; no schema/Docker changes.
- Built both images; recreated **only** `wledger` and `wledger-mcp` (brief interruption). Original dirty checkout untouched.

## Images (old → new)
- wledger: `sha256:5b9e68f2…` → `sha256:aa3bad3bca97c49db7985f5c27d0c26c1f7193ef4d0f7b1ee48e8888b4a034af`
- wledger-mcp: `sha256:21407226…` → `sha256:094c08fb1fd2e26ed7b6bdbe2c0dd6b27c912c5830ef1f2ede41c4b9984612c9`

## Verification (post-deploy) — PASS
- Containers `Up`, RestartCount 0, running; compose `working_dir` = `…/wledger-release-a12d824`.
- `goose: no migrations to run. current version: 10`; **0 ERROR** lines in both.
- `https://storage.localdomain/login` → 200; `/` → 303; API `/api/v1/health` 401 (no token) / 200 (token); MCP loopback `127.0.0.1:9100` 401/200; external `192.168.1.108:9100` blocked.

## State invariants — PASS (pre == post)
- integrity ok, FK clean, goose 10; `led_coordinate_space=drawer`; counts 1/2/68/2/2/15/1; bins mapped 68/0; digest `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09` == baseline.
- Original production checkout preserved (`d7b5690…`).

## Rollback
- **Not used** — deployment and verification succeeded. Assets remain: the `pre035` backup + `rollback-pre035-*` tags + saved image tars.

## Pending
- Browser visual check: production has **0 walls / 0 wall_cards**, so the dashboard renders the legacy view and the wall-modal path is not exercised. No authenticated browser was used and no wall was created. Wall-modal visual check remains **PENDING** (recommend staging). Task 025 Secure-cookie/CSRF browser checks also remain pending.

## Boundaries respected
No LED/WLED commands, no Locate/Global Off, no Home Assistant change, no wall creation, no DB restore, no unrelated service changes, no secrets printed.

## Evidence / SHAs
- Deployed production commit: `a12d824e48da7c19b8ad508027898492b9d84c81`
- Previous production commit: `696475c8d58786f3e0e39c6b9e38e1a412f1f7cb`
- Release path: `/home/spetchal/wledger-release-a12d824`
- Backup: `/home/spetchal/backups/wledger-pre035-20261010T110002Z`
