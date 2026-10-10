# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 7
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 011
Production-Authorization: NONE
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## Summary
Read-only deployment-readiness check for MCP connectivity plus a Stage A backup runbook. No production write was made. origin/main is unchanged at the baseline; AGENTS.md policy confirmed. Task 010 conclusions (B1 benign, B4/B5 verified) re-confirmed.

## 1. MCP consumers (read-only)
VERIFIED
- `wledger-mcp` runs only on the `wledger_default` network; no other container joins it. No running container, compose file, env, systemd unit or cron entry declares a WLEDger MCP client (scanned all containers; none reference wledger/9100).
- No reverse-proxy route targets 9100: nginx-proxy-manager hosts are only `storage.localdomain -> 192.168.1.108:8090` (web) and `theserver.localdomain -> 192.168.1.108:10380`. MCP is not proxied.
- `wledger-mcp` logs contain only the startup line (no per-request logging); no `:9100` entries in proxy access logs.
- MCP is reachable on the LAN (TCP `192.168.1.108:9100` open from the workstation). ufw is inactive; Docker DNAT rules publish 9100. The host also runs Tailscale, so 9100 may be reachable over the tailnet.
- The running MCP is the OLD (`d7b5690`) build: `MCP_HTTP_ADDR=:9100`, `MCP_TRANSPORT=http`, and NO inbound auth (`cmd/mcp-server/auth.go` did not exist at `d7b5690`) → an unauthenticated MCP is exposed on `0.0.0.0:9100`.
UNKNOWN
- Home Assistant (192.168.1.103) dependency: that host is not key-accessible, so its MCP configuration cannot be read.
- A Hermes agent session store contained an MCP-style `/mcp ... 200 OK` record, but the endpoint/port could not be reliably attributed to WLEDger within the read-only budget (oversized DB); treat as UNKNOWN.
- Any other LAN/tailnet client: UNKNOWN.

## 2. Proposed MCP transition (secure, minimally disruptive)
- Do NOT publish an unauthenticated MCP. The baseline already fixes this: it adds inbound bearer + Origin enforcement and defaults to loopback publish.
- Distinguish the two settings: inside-container `MCP_HTTP_ADDR` vs the host publish mapping. Baseline compose sets `MCP_HTTP_ADDR=:9100` inside the container but publishes `127.0.0.1:9100:9100` on the host — the container still listens on all interfaces internally; only host exposure is restricted.
- Options (decision needed):
  (a) If no remote MCP client exists → adopt the baseline compose (loopback publish): lowest exposure, no external access.
  (b) If a remote client (e.g. Home Assistant) exists → do not use a plaintext LAN port; keep a host publish but require the bearer token, and prefer an authenticated HTTPS reverse proxy (add a proxy host for a chosen name → `127.0.0.1:9100`). That client must be updated to send `Authorization: Bearer <WLEDGER_API_TOKEN>`.
- Credentials: MCP uses the same `WLEDGER_API_TOKEN` as `/api/v1` (no separate MCP credential); rotating it affects both.

## 3. Algolia key (safe classification; value NOT disclosed)
VERIFIED
- The key in untracked `debug_algolia.py` is byte-identical to `defaultSearchKey` in the TRACKED public source `internal/suppliers/providers/officeworks/provider.go` (compared by equality only; no value printed).
- Context: used as `X-Algolia-API-Key` against `https://k535caawve-dsn.algolia.net/...` (Algolia query endpoint) for a public product-search index — the same key already present in the public GitHub repo.
INFERRED
- Consistent with an Algolia search-only (public) key, not an admin key.
UNKNOWN
- Definitive ACL cannot be established without the Algolia dashboard.
Recommendation: no emergency rotation attributable to the diagnostic (already public in tracked source); prefer supplying Algolia credentials via configuration rather than hard-coding, and rotate if the key is later found to carry broader ACLs. No files were changed.

## 4. Stage A backup runbook (DRAFT — not executed)
Backup root `/home/spetchal/backups/wledger-<UTCstamp>/` (outside the live dir; `umask 077`). Host `mainserver`; user `spetchal` (docker) + `sudo` (root-owned data/uploads/logs). Free space ~1 TB.
Assets (rollback inventory):
1. Images — record IDs (`wledger-wledger:latest` = `sha256:25560ce6...`; `wledger-mcp-server:latest` = `sha256:e2582152...`); tag as `:rollback-<ts>`; `docker save` each to `images/*.tar` so rollback can redeploy the exact running image without rebuilding.
2. WAL-consistent DB — `sudo python3` SQLite online backup (source opened `mode=ro`) → `db/wledger.db`; verify `integrity_check`=ok, `foreign_key_check` clean, counts (1 controller / 2 drawers / 68 bins / 2 parts / 14 audit), goose=9; record `sha256sum`. No writer stop required. (A file copy of db+wal+shm would require stopping writers — separate gate.)
3. Uploads — `sudo tar -czf uploads/uploads.tar.gz -C /home/spetchal/wledger uploads` (contains user data; secure perms).
4. Config — `docker-compose.yaml`, `.env` (chmod 600, not displayed), `.dockerignore`, `Dockerfile`.
5. Production-only source — `git diff > source/prod-tracked.diff`; `git status --porcelain > source/prod-status.txt`; `tar` of untracked diagnostics (`cmd/alidtest`, `debug_algolia.py`, `scripts/aliexpress_helper.mjs`) into `source/prod-untracked.tar.gz` (secure perms; contains a possible secret). Exclude `data/`, `uploads/`, `logs/`, `.git`, and binaries.
6. Manifests — `sha256sum` all assets to `MANIFEST.sha256`; `provenance.txt` (host, timestamp, image IDs, goose version, counts).
Failure handling: abort Stage A on any error; never overwrite an existing backup directory; keep partials for inspection; do not proceed to Stage B without a verified MANIFEST and a passing DB integrity check.
Verification (no production writes): optionally load the DB snapshot in a disposable directory and boot the saved image against it.

## 5. Decision table
| Item | Status | Evidence |
| --- | --- | --- |
| B1 dirty checkout benign (formatting-only) | VERIFIED | `git diff -w` on production |
| B4 old binary reads goose-v10 DB | VERIFIED (source) | explicit column lists; additive migration 010 |
| B5 old migrations on v10 DB = no-op | VERIFIED (disposable test) | goose err=nil, version stays 10 |
| MCP LAN-reachable and unauthenticated (old build) | VERIFIED | port open; `d7b5690` lacks auth.go |
| No declared MCP consumer | VERIFIED | compose/env/network/proxy/systemd scan |
| HA / other remote MCP dependence | UNKNOWN | host not inspectable; attribution unclear |
| Algolia key == tracked public search key | VERIFIED (equality) | matches `defaultSearchKey` in public source |
| Algolia key ACL (search-only vs admin) | UNKNOWN | needs Algolia dashboard |
| Stage A backup feasible and WAL-consistent | VERIFIED (design) | online-backup API + disk headroom |
| Exact running image preserved | NOT DONE | requires `docker tag/save` (Stage A gate) |

Approval needed for Stage A (all production writes): create backup dir; run the SQLite online backup; tar uploads/config/source; `docker tag` + `docker save`; optionally stop writers if the file-copy method is chosen. The online-backup method needs no restart, deploy or migration.

## Commit SHAs
- Baseline: `63016f4e75134ae0675934818fb77f53dd463800` (origin/main)
- Production HEAD: `d7b5690ca9debd37218468a63c215b5b21fc6c03`
- Handoff tip read: `cecbd4ca6d45def70610ac6d6fb33c72e947cb1b`

## Next Step
Await ChatGPT review and explicit authorization for Stage A only (online backup + image tag/save).
