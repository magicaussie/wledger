# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 19
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 017 — Read-Only LED Coordinate Conversion Preflight
Production-Authorization: READ_ONLY_VERIFICATION_ONLY (consumed)
Current-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Result: GO (no blockers; effectively numerically identity)

## 1. Deployed state (VERIFIED)
- Release HEAD `ca2789f…`; running `wledger` image `sha256:886f3f36…` (RestartCount 0); `wledger-mcp` unchanged (`sha256:70e31ce0…`).
- Backup `wledger-pre016-20261010T043616Z` present and verifies (manifest OK).

## 2. Database state (VERIFIED, read-only)
- goose version 10; integrity_check ok; foreign_key_check CLEAN.
- Flags: `drawer_allocation_backfilled=true`, `migration_005_applied=true`; **no `led_coordinate_space` flag ⇒ segment-relative**.
- Counts: controllers 1, drawers 2, bins 68, parts 2, assignments 2; bins mapped 68 / unmapped 0.
- Bin digest `1b0f9bd7…` (unchanged).
- Allocations: drawer 3 (controller 3, segment 0) led_start 0 / led_count 628; drawer 4 (controller 3, segment 1) led_start 0 / led_count 513. No allocation overlap within a segment.

## 3. Is the preview read-only? (VERIFIED with one caveat)
- `GET /hardware/conversion` → `HandleConversionPreview` only calls `hardware.PreflightConversion`, which builds the report inside a read transaction and performs **no writes** to bins/containers/flags (`internal/hardware/conversion.go` `buildConversionReport`/`PreflightConversion`).
- Caveat (benign side effect, not conversion data): the page also calls `middleware.CSRFToken`, which persists a per-session CSRF token in the session store on first use. That touches only session/CSRF metadata, never LED/bin/conversion data.

## 4. Offline conversion preview (computed read-only; no route called)
Drawers are each the sole occupant of their segment and `led_start = 0`, so every proposed drawer-relative index equals the current segment-relative index (**identity**).
- Drawer 3 (seg 0): 36 mapped bins, from 0..612, **to 0..612**, max end 612+16=628 ≤ 628, out-of-range 0, duplicate 0, unmapped 0. Example: bin 69 `0→0` (w18); bin 104 `612→612` (w16).
- Drawer 4 (seg 1): 32 mapped bins, from 0..492, **to 0..492**, max end 492+21=513 ≤ 513, out-of-range 0, duplicate 0, unmapped 0. Example: bin 105 `0→0` (w10); bin 136 `492→492` (w21).
- Report: TotalDrawers 2, Convertible 2, Blocked 0, AffectedBins 68.
- Reproduced preview fingerprint (same algorithm as `ConversionReport.Fingerprint`): `4477c3b92003ae86ff4422637718d40a21761e239b18016aeb5b4e00b90ac03c`. (Re-confirm live at conversion time; a stale fingerprint is rejected.)

## 5. Physical LED addressing (VERIFIED semantics; one hardware ASSUMPTION)
- `mapper.CalculateGlobalIndex`: segment ⇒ `(segment_id, bin.led_index)`; drawer ⇒ `(segment_id, container.led_start + bin.led_index)`. `LocateBin` is space-aware; `LocateDrawer` uses `(segment_id, led_start, led_count)` directly.
- Because `led_start = 0` for both drawers and the indices do not change, both spaces resolve to the **same physical target** `(segment_id, idx)`. The conversion therefore cannot alter which physical LEDs are addressed, independent of WLED segment offsets (VERIFIED by source).
- ASSUMPTION (not verifiable here, not queried): the WLED device's actual segment configuration matches the app's `segment_id`/`led_count`. This does not affect the no-op conclusion, but would matter for any later change that alters allocations.

## 6. Executor guards (VERIFIED)
- `POST /hardware/conversion` requires admin (`CanConfigure`) + CSRF + `confirm=confirm` + a non-empty preview fingerprint; the engine re-derives the report inside a `BEGIN IMMEDIATE` transaction and is all-or-nothing. It refuses if any drawer is blocked (`ConversionRefused`), if the fingerprint is stale (`ConversionStale`), if already drawer (`AlreadyDrawer`), or if the space is unresolved (error). The audit entry is written in the same transaction.
- The conversion rewrites all 68 bin rows (clear + update) to identical values, then sets the flag to `drawer` and writes an audit row.

## 7. Blockers / conditions
- None found: no unmapped bins, no negative/out-of-bounds indices, no duplicate indices, no allocation overlap, fingerprint reproducible and non-stale. Value-destroying conditions (blocked drawers / overlap) are absent.

## 8. Integrations
- API/MCP locate endpoints are coordinate-space aware, and because every mapped index is numerically unchanged, any consumer (including Home Assistant) sees identical values ⇒ no HA adjustment required. (This holds precisely because `led_start = 0` for both drawers; a future change to allocations would require re-checking consumers.)

## 9. Recommendation
- **GO** for a later, separately authorized conversion. It is effectively a numerical no-op on LED indices (identical values), changes the stored coordinate-space flag to `drawer`, and records an audit entry. No physical addressing change is expected.
- If conversion is desired, prefer it only when the DB will be stable between preview and confirmation (the fingerprint guard rejects a stale confirmation). Not required for correctness.

## 10. Boundaries respected
- Read-only only: no conversion POST, no admin mutation, no migration, no production/backup writes, no hardware/WLED commands, no restarts, no HA changes, no source changes.

## Evidence / SHAs
- Deployed: `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`
- Source inspected: same commit (`internal/hardware/conversion.go`, `internal/handler/conversion.go`, `internal/hardware/mapper/mapper.go`, `internal/wled/service.go`, `internal/middleware/csrf.go`).
- Prior production commit: `63016f4e75134ae0675934818fb77f53dd463800`.
