# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 71
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 041 — Controlled Production Deployment of Main 5acb006
Production-Authorization: USER_EXPLICITLY_AUTHORIZED_THIS_DEPLOYMENT (NOT CONSUMED — no deployment performed)
Expected-Main: 5acb00678da633a40dc6a4e6ac17b60275c40b91
Expected-Running-Release: a12d824e48da7c19b8ad508027898492b9d84c81
Scope: WLEDger app and MCP service ONLY
Result: **STOPPED AT PREFLIGHT — environment does not match the plan.** No backup, no build, no container recreate, no merge, no deployment. Production left exactly as found.

## 1. Decision

The handoff guardrail states: *"STOP if unexpected."* Preflight verification found that the **actual production environment does not match the handoff's expected state** on every material point (running release, live directory, backup/release paths, MCP service, schema version and data baseline). Per the guardrail and the repository policy (*"Stop and report any unexpected branch divergence…"*), I did **not** proceed. No state-changing action was taken.

## 2. Expected vs actual (preflight)

| Item | Handoff expectation | Actual on host | Match |
| --- | --- | --- | --- |
| Running release | `a12d824e48da7c19b8ad508027898492b9d84c81` | checkout HEAD `9264b421e99c88b9d1ee8aeb1149d439dfe36cdc` (2026-08-21) | ✗ (26 commits behind `a12d824`) |
| Live / dirty checkout | `/home/spetchal/wledger` | **does not exist** | ✗ |
| Actual production checkout | — | `/home/spetchal/Documents/New OpenCode Project/wledger` | ✗ (unexpected path) |
| Previous complete backup | `/home/spetchal/backups/wledger-pre035-20261010T110002Z` | `/home/spetchal/backups` **does not exist** | ✗ |
| Isolated release dir | `/home/spetchal/wledger-release-a12d824` | **does not exist** | ✗ |
| App container | `wledger` | `wledger` present, image `sha256:a05770ea6039…` | ~ (image not in store) |
| MCP container | `wledger-mcp` | **not running / not present** | ✗ |
| Schema (goose) | `goose10` | `goose 8` | ✗ |
| Data baseline | 1 controller, 2 containers, 68 bins, 68 mapped, 0 unmapped | **0 controllers, 0 containers, 0 bins, 0 mapped** | ✗ |
| Mapping digest | `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09` | not applicable — no mappings exist | ✗ |
| `.env` | referenced by compose | **absent** | ✗ |

## 3. Actual production evidence (read-only)

**Host / Docker:** Docker 29.1.3, Compose v5.0.0.

**Running container `wledger`:**
- Image: `sha256:a05770ea6039747c6aac52a987b78d3b35b853c7aec38231fc6c6ea6db1dc305`
- Created: `2026-08-15T00:33:27Z`; Status: `Up 6 days`; RestartCount: `0`
- Port: `0.0.0.0:8090->8080/tcp`
- Compose project `wledger`, service `wledger`, working dir `/home/spetchal/Documents/New OpenCode Project/wledger`, config `docker-compose.yaml`
- Mounts: `./data → /wledger/data`, `./uploads → /wledger/app/uploads`, `./logs → /wledger/app/logs`
- **Anomaly:** the running image ID is **not present** in the Docker image store (`docker images -a` has 31 images; `a05770ea6039…` is absent). No `wledger-*:rollback-*` tags exist.

**Compose services:** `docker-compose.yaml` defines `wledger` **and** `mcp-server` (`container_name: wledger-mcp`), but only `wledger` is running. `docker ps -a` shows no `wledger-mcp`.

**Database** (`…/wledger/data/wledger.db`, read-only):
```
integrity_check: ok
journal_mode: wal
goose_version: 8
count_controllers: 0   count_containers: 0   count_bins: 0
count_parts: 0         count_part_assignments: 0   count_audit_logs: 0
count_users: 1
bins_mapped: 0         bins_unmapped: 0
```
The live DB is effectively empty — there are **no LED mappings to preserve or compare**.

**HTTP / HTTPS:**
- `http://localhost:8090/` → `303` → `/login`; `http://localhost:8090/login` → `200`
- `https://storage.localdomain/` → `303`; `https://storage.localdomain/login` → `200`
- MCP `http://localhost:9100/` → **no response** (service not running)

**Container log (tail):** `goose: no migrations to run. current version: 8`; server listening on `:8080`; only 404s for `/v1/models`, `/api/tags`, `/health` etc. from an external prober.

**Stale artifacts:** `/home/spetchal/wledger-handoff/` contains `stageA.sh` and `repo/` (checkout at `fb85926`, Sequence 35 / Task 024 era). `stageA.sh` targets `LIVE=/home/spetchal/wledger` and a `wledger-mcp` container — **neither exists**. These are not a valid basis for the Task 041 procedure.

## 4. Why I stopped

1. The documented procedure (Task 035 reference, `stageA.sh`) targets paths and containers that **do not exist** on this host. Executing it would fail at its first `test -d "$LIVE"` / `docker inspect wledger-mcp` guard.
2. The expected running release (`a12d824`) is **not** what is running (`9264b42`), so the "previous immutable app and MCP images" rollback path cannot be reconstructed as specified.
3. There is **no `wledger-mcp` container**, so "recreate ONLY wledger and wledger-mcp" cannot be performed as written.
4. The expected data baseline (68 bins / 68 mapped / goose10) does **not** exist; the live DB is empty at goose 8. Any "mapping digest unchanged" verification would be meaningless.
5. Adapting the procedure to the real environment would be **improvising a production deployment** against a plan that does not match — explicitly forbidden by the guardrails.

## 5. Actions NOT taken

- No backup directory created (the documented target `/home/spetchal/backups` and its pre035 predecessor do not exist; creating one against a mismatched procedure would imply a verified rollback path that does not exist).
- No release checkout created; no `docker build`; no image tags.
- No container stopped, recreated, pruned or restarted. `wledger` remains `Up` with RestartCount 0.
- No `git push` to `main`; `origin/main` remains `5acb00678da633a40dc6a4e6ac17b60275c40b91`.
- No DB writes, no migrations, no Wall creation, no LED/Locate/Global-Off calls, no Home Assistant changes, no secrets printed.

## 6. Recommended next steps (for ChatGPT / user)

1. Confirm the **intended production host and live directory**. The handoff assumes `/home/spetchal/wledger`; the only WLEDger production stack found is `/home/spetchal/Documents/New OpenCode Project/wledger`.
2. Confirm whether the `mcp-server` service is expected to be running (it is defined in compose but absent).
3. Reconcile the running release: the host runs `9264b42` (Aug 21), not `a12d824`. Decide whether the target is still `5acb006` and whether the 26-commit gap is intended.
4. Re-issue the deployment task with the **correct live path, container set, and a valid baseline** (or explicitly authorize adapting the procedure to the actual environment).
5. Only then take a fresh verified backup and proceed.

## Evidence summary

- Expected `main`: `5acb00678da633a40dc6a4e6ac17b60275c40b91` (unchanged)
- Actual production checkout: `9264b421e99c88b9d1ee8aeb1149d439dfe36cdc` @ `/home/spetchal/Documents/New OpenCode Project/wledger`
- Running container image: `sha256:a05770ea6039747c6aac52a987b78d3b35b853c7aec38231fc6c6ea6db1dc305` (not in image store)
- DB: integrity ok, goose 8, 0 bins / 0 mapped
- MCP: not running
- Backups / release dirs: absent

## Boundaries respected

No production deployment or restart, no live DB change, no Wall creation, no physical LED/WLED calls, no Home Assistant change, no secrets printed, no production checkout edits, no force push. Production is exactly as found. **STOP — awaiting review.**
