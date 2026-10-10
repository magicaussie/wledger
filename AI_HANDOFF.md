# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 73
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 042 — Identify Correct Production Host and Docker Context (READ ONLY)
Production-Authorization: DISCOVERY_ONLY_NO_DEPLOYMENT (respected)
Target-Main: 5acb00678da633a40dc6a4e6ac17b60275c40b91
Result: **CORRECT PRODUCTION HOST FOUND — `mainserver` (192.168.1.108).** Sequence 71 ran on the wrong machine. No deployment, no restart, no DB write, no LED command.

## 1. Root cause: wrong machine

Sequence 71 executed **locally on `spetchal-MS-7D75`** (the workstation), against the **local Docker daemon**, not on the production server.

| Identity check (where Seq 71 ran) | Value |
| --- | --- |
| `hostname -f` | `spetchal-MS-7D75` |
| `hostname -I` | `192.168.1.182 192.168.1.148 172.19.0.1 172.17.0.1 172.20.0.1 172.18.0.1 100.70.139.87` |
| `id` | `uid=1000(spetchal)` (in `docker` group) |
| `DOCKER_HOST` / `DOCKER_CONTEXT` | `unset` / `unset` |
| `docker context show` | `default` (`unix:///var/run/docker.sock`) |
| `docker info` | `spetchal-MS-7D75 /var/lib/docker` |
| `uname -a` | `Linux spetchal-MS-7D75 6.14.0-37-generic … Ubuntu 24.04` |

The workstation happens to run its **own, unrelated** `wledger` container (from `/home/spetchal/Documents/New OpenCode Project/wledger`, HEAD `9264b42`, goose 8, empty DB). That is what Sequence 71 inspected — a different deployment, not production.

## 2. Correct production host verified

`192.168.1.108` is a **separate LAN host** (MAC `24:4b:fe:df:7e:5c`), not a local interface. SSH to `spetchal@192.168.1.108` succeeded with **`StrictHostKeyChecking=yes`** and `BatchMode=yes` — i.e. the host key was **already trusted** (present as a hashed entry in `~/.ssh/known_hosts`), so no unknown key was accepted. `hostname -f` returned **`mainserver`**.

| Identity check (mainserver) | Value |
| --- | --- |
| `hostname -f` | `mainserver` |
| `hostname -I` | `192.168.1.108 100.98.254.48 …` |
| `id` | `uid=1000(spetchal)` (in `docker` group) |
| `docker context show` | `default` |
| `docker info` | `mainserver /var/lib/docker` |
| `uname -a` | `Linux mainserver 7.0.0-31-generic … Ubuntu` |
| Disk | `/` 1.9T, **1016G free** |

## 3. Comparison: Sequence 71 host vs production

| Item | Seq 71 host (`spetchal-MS-7D75`) | Production (`mainserver` 192.168.1.108) |
| --- | --- | --- |
| Live / dirty checkout | `/home/spetchal/Documents/New OpenCode Project/wledger` @ `9264b42` | `/home/spetchal/wledger` @ `d7b5690` (untouched) |
| Release dir | absent | `/home/spetchal/wledger-release-a12d824` @ **`a12d824`** |
| Backups | absent | `/home/spetchal/backups/wledger-pre035-20261010T110002Z` (root:root 700) |
| App container | `wledger` (image `a05770ea…`, not in store) | `wledger` (image `aa3bad3bca97…`) |
| MCP container | **absent** | `wledger-mcp` (image `094c08fb1fd2…`) |
| goose version | 8 | **10** |
| Data baseline | 0 controllers / 0 containers / 0 bins | **1 / 2 / 68**, 68 mapped, 0 unmapped |
| Coordinate space | n/a | `drawer` |
| `.env` | absent | `/home/spetchal/wledger/.env` (present) |

## 4. Production configuration (documented, read-only)

**Containers** (both `Up 3 hours`, `RestartCount 0`):
- `wledger` — image `sha256:aa3bad3bca97c49db7985f5c27d0c26c1f7193ef4d0f7b1ee48e8888b4a034af`, tag `wledger-wledger:latest`, port `0.0.0.0:8090->8080/tcp`
- `wledger-mcp` — image `sha256:094c08fb1fd2e26ed7b6bdbe2c0dd6b27c912c5830ef1f2ede41c4b9984612c9`, tag `wledger-mcp-server:latest`, port `127.0.0.1:9100->9100/tcp`

**Compose:** project `wledger`, `working_dir` = `/home/spetchal/wledger-release-a12d824`, config = `…/docker-compose.yaml`. App binds `…/wledger-release-a12d824/{data,logs,uploads}` (symlinks → `/home/spetchal/wledger/{data,logs,uploads}`); MCP uses anonymous volumes.

**Release:** `/home/spetchal/wledger-release-a12d824`, detached HEAD = `a12d824e48da7c19b8ad508027898492b9d84c81` (matches Expected-Running-Release). Working tree clean except untracked runtime dirs (`data`, `logs`, `uploads`).

**Backups** (`/home/spetchal/backups/`, root-protected): `wledger-pre035-20261010T110002Z` (complete), plus `wledger-20261010T040208Z`, `wledger-pre016-…`, `wledger-pre019-…`, `wledger-pre025-…`, `wledger-pre028-…`, and the known-incomplete `wledger-pre035-20261010T105604Z`.

**Rollback tags present:** `wledger-wledger:rollback-pre035-20261010T110002Z` (`5b9e68f24f47`), `wledger-mcp-server:rollback-pre035-20261010T110002Z` (`21407226a97b`), plus older `pre028`/`pre025`/`pre016`/`20261010T040208Z` tags.

**Database** (`…/wledger-release-a12d824/data/wledger.db`, read-only):
```
integrity_check: ok      journal_mode: wal      goose_version: 10
count_controllers: 1     count_containers: 2    count_bins: 68
count_parts: 2           count_part_assignments: 2   count_audit_logs: 15
count_users: 1           bins_mapped: 68        bins_unmapped: 0
system_flags: led_coordinate_space=drawer, drawer_allocation_backfilled=true, migration_005_applied=true
```
This **matches the Task 035 baseline exactly** (1/2/68/2/2/15/1, 68 mapped, 0 unmapped, goose 10, coordinate space `drawer`).

**Health (read-only):**
- `http://localhost:8090/` → `303` → `/login`; `/login` → `200`
- `https://storage.localdomain/login` → `200`
- MCP loopback `http://127.0.0.1:9100/mcp` → `401` (unauthorized, as expected)
- MCP external `http://192.168.1.108:9100/mcp` → **blocked** (no route)

## 5. Mapping digest — limitation

The Task 035 mapping digest `1b0f9bd7b09223548df0f7097bec0ffa8b57d0c85f31979ad2c5db821ece8f09` could **not be independently reproduced**: the digest algorithm is **not documented** in the handoff history or anywhere in the release codebase (the only hashing helper, `ConversionReport.Fingerprint`, is for conversion reports, not mappings). I tested ~30 plausible canonicalisations of the `bins`/`containers`/`controllers` data (id/container/led/width/grid variants, JSON, CSV, `key=value;` style) — none matched.

**Mitigation:** the underlying mapping data matches the baseline on every directly observable invariant — counts (1 controller / 2 containers / 68 bins), 68 mapped / 0 unmapped, `led_coordinate_space=drawer`, goose 10, integrity ok. The digest value itself should be re-derived from the original Task 035 script if exact equality is required.

## 6. Are the Task 041 prerequisites valid?

**Yes — now that the correct host is identified.** On `mainserver` (192.168.1.108):
- Running release `a12d824` ✓ (matches Expected-Running-Release)
- Release dir `/home/spetchal/wledger-release-a12d824` ✓
- Complete pre035 backup `/home/spetchal/backups/wledger-pre035-20261010T110002Z` ✓
- Both `wledger` and `wledger-mcp` running ✓
- goose 10, 68 mapped bins, coordinate space `drawer` ✓
- Rollback images/tags present ✓

The Task 041 plan is executable **against `mainserver` over SSH**, not against the local workstation. The earlier STOP was correct for the machine it ran on; the plan itself is sound for the real host.

## 7. Recommended for the (future) deployment task

1. Run all Task 041 steps **on `mainserver` via `ssh spetchal@192.168.1.108`** (or an equivalent verified remote Docker context), never on `spetchal-MS-7D75`.
2. Re-derive the mapping digest with the original Task 035 method before/after so the "digest unchanged" invariant is meaningful.
3. Note the workstation's unrelated `wledger` container is **not** production and must not be touched.
4. Deployment authorization remains **pending** — this task was discovery only.

## Evidence summary

- Wrong host (Seq 71): `spetchal-MS-7D75` (192.168.1.182), local Docker `default` context
- Correct host: `mainserver` (192.168.1.108), verified via strict-host-key SSH
- Release: `/home/spetchal/wledger-release-a12d824` @ `a12d824e48da7c19b8ad508027898492b9d84c81`
- App image: `sha256:aa3bad3bca97c49db7985f5c27d0c26c1f7193ef4d0f7b1ee48e8888b4a034af`
- MCP image: `sha256:094c08fb1fd2e26ed7b6bdbe2c0dd6b27c912c5830ef1f2ede41c4b9984612c9`
- Backup: `/home/spetchal/backups/wledger-pre035-20261010T110002Z`
- DB: integrity ok, goose 10, 1/2/68, 68 mapped / 0 unmapped, space `drawer`

## Boundaries respected

Read-only only. No `docker compose up/down/build/restart`, no image tags/prune, no backup or release creation, no DB writes, no schema changes, no LED commands, no HA changes, no edits to any checkout, no secrets printed. **STOP — awaiting review.**
