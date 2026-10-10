# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 9
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 012 — Stage A Production Backup
Production-Authorization: STAGE_A_BACKUP_ONLY (consumed)
Baseline: 63016f4e75134ae0675934818fb77f53dd463800

## Outcome
Stage A completed successfully and verified. No Stage B action was taken; production services were not restarted and their state is unchanged.

## Backup directory
`/home/spetchal/backups/wledger-20261010T040208Z` (mode 700, root-owned, outside the live repo, not under Git).

## Assets and sizes (bytes)
- images/wledger-wledger_20261010T040208Z.tar — 468,335,104
- images/wledger-mcp-server_20261010T040208Z.tar — 468,335,104
- db/wledger.db — 9,711,616 ; db/verification.txt — 296
- uploads/uploads.tar.gz — 134,962
- config/ — docker-compose.yaml, Dockerfile, .dockerignore, .env (secret, mode 600), .gitignore
- source/ — HEAD.txt, remotes.txt, status-porcelain.txt, prod-tracked.diff, prod-untracked-diagnostics.tar.gz
- provenance.txt, RESTORE_NOTES.md, MANIFEST.sha256
- Total backup size ~903 MiB; free space after: 1022 GiB.

## Images preserved by immutable ID
- wledger: container `78fe24849ac5…`, image `sha256:25560ce66e2802ccd56aa17d63452bf3048fbb01a53a466a9fa3be3befebf58a`, tag `wledger-wledger`
- wledger-mcp: container `9da475efc716…`, image `sha256:e25821527243afafc758a1c19a25be78a34dfd233c87614838d23bac875e1746`, tag `wledger-mcp-server`
- Rollback tags added to those verified IDs: `wledger-wledger:rollback-20261010T040208Z`, `wledger-mcp-server:rollback-20261010T040208Z`
- Saved via `docker image save`; archive readability confirmed with `tar -tf`.

## Database verification (snapshot opened read-only)
- integrity_check: ok
- foreign_key_check: CLEAN
- journal_mode: wal; snapshot ships as a single self-contained `wledger.db` (a 0-byte `-wal` and empty `-shm` were removed only after confirming the main file is complete)
- goose version: 9
- Current counts: controllers 1, drawers/containers 2, bins 68, parts 2, part_assignments 2, audit_logs 14, users 1; bins mapped 68 / unmapped 0
- system_flags: `migration_005_applied=true` (only row); no `led_coordinate_space` flag
- Independently re-verified: copying ONLY `db/wledger.db` to a scratch path reproduced an ok integrity_check and identical counts.

## Source/config captured
- Git HEAD `d7b5690ca9debd37218468a63c215b5b21fc6c03`; remotes recorded.
- Working-tree status recorded (filenames only): 5 tracked modifications/deletion (the tracked `.go` edits were previously shown to be formatting-only) plus 6 untracked paths; untracked diagnostics preserved in `source/prod-untracked-diagnostics.tar.gz`. Contents withheld — no diffs, secrets, or user data disclosed.

## Manifest / integrity
- `MANIFEST.sha256` covers all 17 files; `sha256sum -c` reports OK for every file. Backup dir is root-owned mode 700, outside the live repo, and untracked by Git.

## Container state (unchanged)
- `wledger` and `wledger-mcp` remained "Up 16 hours" throughout. No restart, stop, migration, deploy, LED command, key rotation, or MCP change was performed.

## Exact actions performed
1. Preflight: verified host/paths, container IDs, image IDs/tags; confirmed no pre-existing `wledger-*` backup.
2. Created the timestamped backup dir (umask 077, mode 700).
3. `docker tag` (rollback tags) + `docker image save` for both images by immutable ID; verified readability.
4. SQLite online backup API (source `mode=ro`) → `db/wledger.db`; verified; then removed the empty WAL artifacts.
5. tar of uploads; copied config (incl. `.env`, mode 600); recorded git HEAD/remotes/status/diff and untracked diagnostics.
6. Wrote provenance.txt, RESTORE_NOTES.md, MANIFEST.sha256; verified with `sha256sum -c`.
7. Confirmed free space and that both containers remain up.

## Failures / anomalies
- One transient script error: a manifest-regeneration `cd` failed because the backup dir is root-owned; re-run under `sudo` and the manifest verifies cleanly. No production impact.

## Risks
- `uploads/uploads.tar.gz` is not point-in-time synchronized with the DB snapshot (both captured ~2026-10-10T04:02Z; the DB is a consistent online snapshot).
- The backup contains secrets (`.env`) and user data (uploads); it is root-owned mode 700 and must never be committed or published.
- Empty WAL artifacts were removed from the snapshot only after proving the single file is self-contained.

## Next approval gate
Stage B (application upgrade) requires a NEW explicit user approval after ChatGPT reviews these Stage A results. No upgrade was attempted.
