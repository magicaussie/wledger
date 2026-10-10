# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 75
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 043 — Resume Authorized Deployment on Verified mainserver
Production-Authorization: USER_EXPLICITLY_AUTHORIZED_TASK041_DEPLOYMENT (consumed)
Target-Host: spetchal@192.168.1.108
Expected-Hostname: mainserver
Deployed-Commit: 5acb00678da633a40dc6a4e6ac17b60275c40b91
Previous-Commit: a12d824e48da7c19b8ad508027898492b9d84c81
Release-Path: /home/spetchal/wledger-release-5acb006
Backup-Path: /home/spetchal/backups/wledger-pre043-20261010T135548Z
Result: **SUCCESS — deployed and verified (no rollback).** All mapping/DB/HTTP/MCP/CSS invariants PASS.

## 1. Host guard (before any mutation)

All commands ran over `ssh -o StrictHostKeyChecking=yes -o BatchMode=yes spetchal@192.168.1.108`. Guard passed:

| Guard | Result |
| --- | --- |
| `hostname -s` == `mainserver` | ✓ |
| `docker info --format '{{.Name}}'` == `mainserver` | ✓ |
| release `a12d824` HEAD == `a12d824…` | ✓ |
| `docker inspect wledger wledger-mcp` | ✓ |

## 2. Preflight

- `origin/main` == `5acb00678da633a40dc6a4e6ac17b60275c40b91` (target).
- Diff `a12d824..5acb006` = `.github/workflows/ci.yml`, `.gitignore`, `package.json`, `scripts/wall-browser/*` (test harness), `web/pages/dashboard_wall_*_test.go`, `web/static/css/output.css`. **No migrations, schema, SQL, auth, service-wiring, compose, Dockerfile or LED-logic changes.**
- `docker-compose.yaml` and `Dockerfile` byte-identical between the `a12d824` and `5acb006` releases.
- Pre-deploy containers: `wledger` (`aa3bad3bca97…`) + `wledger-mcp` (`094c08fb1fd2…`), both `Up`, RestartCount 0; app `0.0.0.0:8090->8080`, MCP `127.0.0.1:9100->9100`; `data`/`logs`/`uploads` symlinks → `/home/spetchal/wledger/{data,logs,uploads}`.
- Exact compose invocation confirmed by reproducing the container's `config-hash` (`9a20a961…`) with `docker compose -p wledger --env-file /home/spetchal/wledger/.env`.

## 3. Mapping fingerprint (deterministic, documented)

Method (script `/home/spetchal/wledger-mapping-fingerprint.py`): open the DB `mode=ro`; emit canonical JSON lines for `controllers`, `containers`, `bins` ordered by primary key, plus the mapping-table `CREATE TABLE` definitions; SHA256 the canonical text. Mutable non-mapping fields (`created_at`, `updated_at`, `is_online`) excluded.

| | SHA256 |
| --- | --- |
| **Before** (`/home/spetchal/mapping-before.txt`) | `60e560c35633989f41fdf3beba65e933b82915fc80d917b493e8ab55a86e2f16` |
| **After** (`/home/spetchal/mapping-after.txt`) | `60e560c35633989f41fdf3beba65e933b82915fc80d917b493e8ab55a86e2f16` |
| Result | **IDENTICAL** (rows: 1 controller / 2 containers / 68 bins) |

The legacy Task 035 digest `1b0f9bd7…` is retained as historical reference only (its algorithm remains undocumented; not directly comparable).

## 4. Backup (pre043, root-protected)

`/home/spetchal/backups/wledger-pre043-20261010T135548Z` (root:root, mode 700). Contents: online SQLite snapshot (`db/wledger.db`, self-contained — WAL empty, verified with `immutable=1`), `db/verification.txt`, uploads tar, `config/` (compose, Dockerfile, `.dockerignore`, `.gitignore`, `.env`), `source/` provenance, `mapping/` (before snapshot + fingerprint), `provenance.txt`, `RESTORE_NOTES.md`, `MANIFEST.sha256`.

- `sha256sum -c MANIFEST.sha256` → **all OK** (22 files).
- Snapshot DB: integrity ok, FK clean, goose 10, counts 1/2/68/2/2/15/1, mapped 68/0.
- Rollback tags: `wledger-wledger:rollback-pre043-20261010T135548Z` (= `aa3bad3bca97…`), `wledger-mcp-server:rollback-pre043-20261010T135548Z` (= `094c08fb1fd2…`) — both match the exact pre-deploy running images.
- Previous complete `pre035` backup preserved; incomplete `pre035-…T105604Z` untouched.

## 5. Release and build

- New isolated release `/home/spetchal/wledger-release-5acb006` (git clone, detached HEAD `5acb006`), `data`/`logs`/`uploads` symlinked to the shared dirs (unchanged).
- Built both images from the exact SHA: `docker compose -p wledger --env-file /home/spetchal/wledger/.env build wledger mcp-server`.
- Runtime CSS in the new image: **129722 bytes, 1 line (minified)**, `.gap-x-4` and `.gap-y-1` present — **byte-identical to the running `a12d824` production CSS**.

## 6. Cutover

```
cd /home/spetchal/wledger-release-5acb006
docker compose -p wledger --env-file /home/spetchal/wledger/.env up -d --no-deps wledger mcp-server
```
Only `wledger` and `wledger-mcp` were recreated (no broad `down`/prune). Ports and mounts unchanged.

## 7. Images (old → new)

| Service | Old | New |
| --- | --- | --- |
| wledger | `sha256:aa3bad3bca97c49db7985f5c27d0c26c1f7193ef4d0f7b1ee48e8888b4a034af` | `sha256:1816e335895ce1e9f6a03d17c30c6c84265a2440d22727e8112082d723e80cb9` |
| wledger-mcp | `sha256:094c08fb1fd2e26ed7b6bdbe2c0dd6b27c912c5830ef1f2ede41c4b9984612c9` | `sha256:792152f39dc05ef727e779de8f80413573e1b70683a3540a9942627eec051193` |

## 8. Post-deploy verification — PASS

- Containers `wledger`/`wledger-mcp` `running`, **RestartCount 0**; compose `working_dir` = `/home/spetchal/wledger-release-5acb006`.
- Logs: `goose: no migrations to run. current version: 10`; `Server listening`; MCP `listening on :9100`. No ERROR lines.
- HTTPS: `https://storage.localdomain/login` → `200`; `/` → `303`. HTTP `:8090` same.
- Auth: `/parts` (no auth) → `303`; API `/api/v1/health` → `401` (no token) / `200` (token).
- MCP: loopback `127.0.0.1:9100/mcp` → `401` (no token) / `200` (token); external `192.168.1.108:9100` → **blocked**.
- CSS: `129722` bytes, minified, `.gap-x-4`/`.gap-y-1` present.
- DB: integrity ok, FK clean, goose 10; counts 1/2/68/2/2/15/1; mapped 68 / unmapped 0; `led_coordinate_space=drawer`.
- **Mapping fingerprint before == after** (`60e560c3…`).

## 9. Rollback

**Not used** — all invariants passed. Assets remain available: `pre043` backup + `rollback-pre043-*` tags + saved image tars.

## 10. Boundaries respected

Workstation `wledger` container untouched (`Up 6 days`); original dirty checkout `/home/spetchal/wledger` untouched (`d7b5690…`); the other 52 mainserver containers untouched; no Home Assistant change; no LED/Locate/Global-Off commands; no new Walls; no DB restore; no secrets printed.

## 11. Remaining issues / observations

1. **Committed vs Docker CSS differ.** The committed `web/static/css/output.css` (174681 B, from the Task 039 full-repo build) differs from the Docker-generated runtime CSS (129722 B) because Tailwind v4 auto-detects content across the whole repo locally but only `web/` inside the Docker `css-builder` stage. Production is unaffected (it uses the Docker-built CSS), but the committed artifact and the CI determinism check do not reflect the Docker output. Worth reconciling in a future task.
2. Legacy Task 035 mapping digest algorithm remains undocumented (see §3).

## Evidence / SHAs

- Deployed commit: `5acb00678da633a40dc6a4e6ac17b60275c40b91` (previous `a12d824…`)
- Release: `/home/spetchal/wledger-release-5acb006`
- Backup: `/home/spetchal/backups/wledger-pre043-20261010T135548Z`
- Mapping fingerprint: `60e560c35633989f41fdf3beba65e933b82915fc80d917b493e8ab55a86e2f16` (before == after)
- App image: `sha256:1816e335895ce1e9f6a03d17c30c6c84265a2440d22727e8112082d723e80cb9`
- MCP image: `sha256:792152f39dc05ef727e779de8f80413573e1b70683a3540a9942627eec051193`

**STOP — awaiting review.**
