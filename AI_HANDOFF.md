# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 57
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 034 — Read-Only Predeployment Release Assessment
Production-Authorization: READ_ONLY_ONLY (respected)
Target-Main: a12d824e48da7c19b8ad508027898492b9d84c81
Current-Production: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Result: ASSESSMENT COMPLETE — READY FOR SEPARATELY AUTHORIZED DEPLOYMENT (browser visual check pending)

## 1. Git and production provenance (read-only)
- `origin/main` = `a12d824e48da7c19b8ad508027898492b9d84c81` (target).
- Production isolated release `/home/spetchal/wledger-release-696475c` HEAD = `696475c8d58786f3e0e39c6b9e38e1a412f1f7cb`.
- Containers `wledger` / `wledger-mcp`: `Up`, RestartCount 0, running; images `sha256:5b9e68f2…` / `sha256:21407226…`; compose `working_dir` = `/home/spetchal/wledger-release-696475c`, `config_files` = `…/wledger-release-696475c/docker-compose.yaml`, `environment_file` = `/home/spetchal/wledger/.env`.
- Original dirty checkout `/home/spetchal/wledger` HEAD = `d7b5690…` (untouched).
- Disk: 1017G free (44% used). Rollback tags present: `rollback-pre028-20261010T093605Z` (wledger `e2efa2fb…`, mcp `a88c59ea…`), plus pre025/pre016/20261010T040208Z.
- Backups (root:root, mode 700): `wledger-20261010T040208Z`, `wledger-pre016-…`, `wledger-pre019-…`, `wledger-pre025-…`, `wledger-pre028-20261010T093605Z` (manifest `sha256sum -c` OK).

## 2. Target delta (696475c..a12d824)
3 commits: `126e857` (wall modal a11y), `e14c666` (phrasing content + localized label), `a12d824` (CI workflow).
14 files:
- `.github/workflows/ci.yml` (new)
- `locales/active.{en,de,es,fr,it,pt-BR,ru,zh}.json` (additive keys `NoBinsMapped`, `OpenContainer`)
- `web/components/dashboard_grid.templ` (+ `_templ.go`)
- `web/components/dashboard_wall.templ` (+ `_templ.go`)
- `web/components/dashboard_render_test.go`

**No** schema/migration, SQL, auth, CSRF, router, middleware, `go.mod`/`go.sum`, Dockerfile/compose, `internal/wled`/`hardware`/`ledspace` changes (checked by path). No server-side Go logic changes — only generated templ Go and a test file. The functional change is UI-only: the wall card trigger is a semantic `<button>` with phrasing-only descendants, the modal is a sibling addressed by a scoped Alpine `x-ref`, the modal-box scrolls vertically, an empty-bin state is shown, and the trigger label is localized via `i18n.TD`. Locale changes are additive.

## 3. CI evidence
- Push run `38045351289` (event `push`, branch `main`, headSha `a12d824`): completed, **success** (2m5s).
- PR run `38044812260` (event `pull_request`, headSha `a12d824`): completed, **success**.
- Commit check-runs for `a12d824`: two `validate` runs, both completed/success.
- Local read-only validation on main: `go build -tags fts5 ./...` OK, `go vet -tags fts5 ./...` clean, `web/components` + `internal/dashboard` tests pass.

## 4. Database and LED mapping invariants (read-only)
- `integrity_check` ok; `foreign_key_check` CLEAN; goose 10.
- `led_coordinate_space = drawer`; flags `drawer_allocation_backfilled=true`, `migration_005_applied=true`.
- Counts: controllers 1, containers 2, bins 68, parts 2, part_assignments 2, audit_logs 15, users 1.
- Bins mapped 68 / unmapped 0.
- Bin LED mapping digest `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09` == baseline. No secrets or session identifiers exposed.

## 5. Proposed deployment runbook — PROPOSED ONLY, DO NOT RUN
```bash
# ===== PROPOSED ONLY — requires separate explicit authorization =====
set -euo pipefail
TARGET=a12d824e48da7c19b8ad508027898492b9d84c81
SHORT=a12d824
TS=$(date -u +"%Y%m%dT%H%M%SZ")
LIVE=/home/spetchal/wledger
NEW=/home/spetchal/wledger-release-$SHORT
OLD=/home/spetchal/wledger-release-696475c

# 0. Preflight (read-only)
git -C "$OLD" rev-parse HEAD                                   # expect 696475c…
git ls-remote https://github.com/magicaussie/wledger.git main  # expect a12d824…
docker ps --filter name=wledger --format '{{.Names}} {{.Image}} {{.Status}}'
df -h /home/spetchal

# 1. Pre-deploy backup (root-owned, mode 700) — reuse the Task 025/028 procedure:
#    online SQLite backup API from a mode=ro source, images saved by immutable ID
#    + rollback tags, uploads tar, config incl. .env (mode 600), source provenance,
#    MANIFEST.sha256 + `sha256sum -c`, RESTORE_NOTES.md.
#    Then verify the snapshot: integrity ok, FK clean, goose 10, counts, digest == baseline.
sudo -n bash /path/to/predeploy_backup.sh   # -> /home/spetchal/backups/wledger-pre0NN-$TS

# 2. Isolated release pinned to target
git clone https://github.com/magicaussie/wledger.git "$NEW"
git -C "$NEW" checkout "$TARGET"
ln -s "$LIVE/data"    "$NEW/data"
ln -s "$LIVE/logs"    "$NEW/logs"
ln -s "$LIVE/uploads" "$NEW/uploads"
diff "$OLD/docker-compose.yaml" "$NEW/docker-compose.yaml"   # expect identical
diff "$OLD/Dockerfile"          "$NEW/Dockerfile"           # expect identical

# 3. Build and recreate ONLY the WLEDger project
cd "$NEW"
docker compose -p wledger --env-file "$LIVE/.env" -f docker-compose.yaml build
docker compose -p wledger --env-file "$LIVE/.env" -f docker-compose.yaml up -d

# 4. Verify (health + invariants)
docker inspect -f 'img={{.Image}} rc={{.RestartCount}} st={{.State.Status}}' wledger
docker inspect -f 'img={{.Image}} rc={{.RestartCount}} st={{.State.Status}}' wledger-mcp
docker logs --tail 30 wledger     # expect "goose: no migrations to run. current version: 10", 0 ERROR
curl -sk -o /dev/null -w '%{http_code}\n' https://storage.localdomain/login   # 200
curl -s  -o /dev/null -w '%{http_code}\n' http://localhost:8090/api/v1/health # 401 (200 with token)
# MCP: loopback 127.0.0.1:9100 401/200; external 192.168.1.108:9100 blocked
# DB: integrity ok, FK clean, goose 10, counts 1/2/68/2/2/15/1, digest == 1b0f9bd7…

# 5. Rollback (ONLY on failure) — no destructive DB restore without separate approval
docker tag wledger-wledger:rollback-pre0NN-$TS   wledger-wledger:latest
docker tag wledger-mcp-server:rollback-pre0NN-$TS wledger-mcp-server:latest
cd "$OLD"
docker compose -p wledger --env-file "$LIVE/.env" -f docker-compose.yaml up -d
# recheck health + mapping digest
```

## 6. Non-hardware browser smoke test (user-driven; no LED actions)
1. Log in at `https://storage.localdomain/` (user-driven; no credentials shared with the agent).
2. Dashboard legacy view (0 walls): long names truncate with full text on hover; empty controller shows "No containers configured.".
3. Wall view (if walls exist): open a container card modal; confirm ✕ and backdrop close it; keyboard Tab+Enter opens a card; a tall grid scrolls; check desktop and mobile widths.
4. Confirm no console errors and no duplicate `id="container_modal_*"` in the DOM.
5. Do **not** create/modify walls or trigger Locate/Global Off (physical LEDs) without explicit authorization.

## 7. Hazards and outstanding validation
- **Hazard:** the delta is UI-only, but the wall modal path is not exercised by the current production data (0 walls), so a regression there would not be visible in production until a wall is created. Mitigated by render tests + the pending visual check.
- **Hazard:** `docker compose up -d` briefly interrupts `wledger` (~1–2 s) during recreate.
- **Hazard:** the CI workflow file is inert for production (no runtime effect); it only adds GitHub Actions runs.
- **Outstanding:** browser visual verification of the Task 030 wall modal is still pending; the Task 025 Secure-cookie/CSRF browser checks also remain pending.
- No migration is expected (goose stays 10); if the deployed container ever reports a migration, STOP and roll back.

## Recommendation
**READY for a separately authorized deployment.** The delta is UI-only with no schema/migration/auth/routing/dependency changes; CI is green on the exact target SHA `a12d824`; and DB/LED-mapping invariants match the established baseline. Deployment remains a separate, explicitly authorized step; the browser visual check should be performed in the deployment window.

## Evidence / SHAs
- Target main: `a12d824e48da7c19b8ad508027898492b9d84c81`
- Current production: `696475c8d58786f3e0e39c6b9e38e1a412f1f7cb`
- Push run: https://github.com/magicaussie/wledger/actions/runs/38045351289
- PR run: https://github.com/magicaussie/wledger/actions/runs/38044812260
- Mapping digest baseline: `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09`
