# WLEDger Active Development

## Current task and Git baseline

- Task: recover the continuation checkpoint and complete controller output, cabinet/drawer mapping, WLED safety/recovery, and inventory/backup acceptance.
- Authorized branch: `development/chatgpt-m6-m7`.
- Current local HEAD: `41d9506df896c9dc0a22d57d341fe8a02042b8ea` (`feat: add category-aware product CSV`).
- Current `origin/development/chatgpt-m6-m7`: `41d9506df896c9dc0a22d57d341fe8a02042b8ea`; fetched and confirmed 0 ahead / 0 behind.
- Owner-provided published baseline: `46aec54fa6a97c59f34776add009568b18991783`.
- Published commits since that baseline: `41d9506df896c9dc0a22d57d341fe8a02042b8ea`.
- Implementation baseline SHA before this checkpoint: `41d9506df896c9dc0a22d57d341fe8a02042b8ea`; the reviewed source changes are currently in the working tree and are not yet committed. The implementation commit SHA will be recorded immediately after commit, followed by a handover-only status commit.
- Owner authorization covers implementation, isolated testing, and publication to the authorized development branch. No production/staging DB, deployment, main merge/release, LED coordinate conversion, or real WLED controller operations are authorized.

## Safety and architecture invariants

- Inventory IDs and quantities are independent of cabinet/drawer geometry, output topology, and pixel allocation.
- Physical cabinets/drawers/bins remain distinct from legacy electrical containers/segments.
- Controller output topology, cabinet path geometry, and drawer pixel allocations are separate models.
- Drawers may own multiple non-contiguous cabinet-wide, 1-based pixel ranges. Ranges are checked for overlap and capacity and resolve to controller-local/WLED segment ranges.
- A saved physical Locate persists target controller, endpoint, mapping fingerprint, pixel ranges, commands, and conservative `may_have_received` ownership before network activation. Stop/recovery only uses saved targeted range-off commands.
- WLED writes, Locate/session transitions, and configuration mutations use SQLite `BEGIN IMMEDIATE` operation claims and durable ownership checks. Conflicts fail closed. Global Off is a separate explicit device-wide action, rejected while physical Locate owns pixels.
- Physical output remains disabled by default. Automated protocol, framebuffer, crash, and multi-process checks use isolated loopback simulators only.
- `.dockerignore` now excludes `.task055b-staging/`; never inspect, modify, or commit its contents.

## Implemented and verified in the current working tree

### Hardware controller configuration

- Migration `024_controller_output_configuration.sql` adds controller type, connection timeout, and persistent `controller_outputs` with stable IDs, ordered output names, pixel counts, direction, linear/serpentine/custom pixel layouts, and cabinet-output projection links.
- Hardware add/edit now owns connection settings and output configuration. It validates supported controller types, output-count capability limits, timeout bounds, grid dimensions, custom permutations, per-output pixel bounds, and total capacity. Output definitions are previewed visually and saved atomically.
- `cabinetpixels.SplitTopologyRange` now applies linear, reverse, serpentine-row, serpentine-column, and custom logical-to-physical pixel transforms; tests cover pattern mapping and invalid custom permutations.
- Cabinet configuration assigns controllers and edits physical output paths only. It no longer duplicates controller output count/length/order settings. Assigning a legacy controller safely projects its existing output capacity into cabinet path rows.
- Drawer allocations support multiple non-contiguous ranges, validate cabinet capacity and overlap/conflicts, preserve allocation identity, and provide cabinet/controller/output previews.

### WLED behavior and safety

- Multi-controller Locate/Stop maps saved drawer ranges to exact WLED segment-relative commands. Global Off remains a separate device-wide command.
- Tests cover exact controller JSON payloads, all simulated framebuffer pixels for Global Off, rejection of Global Off during active Locate, multi-controller Locate/Stop, conflicting concurrent Locate/Stop, cross-connection/process operation claims, controller disconnection, dropped responses, restart recovery, and preservation of unrelated pixels.
- Startup recovery and retry retain unresolved durable ownership and never use Global Off.

### Inventory, categories, CSV, and backup/restore

- The published `41d9506` checkpoint carries hierarchical category-aware product CSV behavior. Existing category images, QR/Code 128 labels, category specifications, general product descriptors, units, purchasing details, search/filtering, and CSV round trips remain covered by the suite.
- Backup envelope version is now `2.3`, carrying controller outputs and controller type/timeout. Restore preserves those records and validates output IDs/order/capacity/custom mappings/projection links before touching the target. Legacy 1.0 and current 2.0/2.1/2.2 archives remain supported; legacy restore synthesizes linear outputs from physical strings or controller capacity.
- Fresh-install and separate-destination browser restore tests confirm migrations 1–24, output configuration, category/product data, and stable physical/inventory identities.

## Database and migration state

- Latest schema migration: `024_controller_output_configuration.sql`.
- Migration sequence present and verified on fresh install: `001` through `024`.
- Controller outputs are distinct from cabinet path sections and legacy containers. Cabinet strings may link to their controller output; legacy path records are preserved and synthesized during older-format restores.
- Current backup format: `storage_data.json` version `2.3`; compatibility for `2.0`, `2.1`, `2.2`, and legacy `restore_data.json` version `1.0` is retained.

## Verification evidence (re-run 2026-10-15; all database/controller fixtures disposable)

- Current source tree is based on Git SHA `41d9506df896c9dc0a22d57d341fe8a02042b8ea`; implementation commit SHA is pending and will be added in the follow-up handover update.
- `git diff --check`: PASS.
- Go 1.26.9 using the disposable `golang:1.26.9` Docker toolchain: `go test -tags fts5 ./...` PASS; `go test -race -tags fts5 ./...` PASS; `go vet -tags fts5 ./...` PASS; server and MCP `go build -tags fts5` PASS. Temporary binaries were removed.
- Node 22.23.2: `npm test` PASS (20 grid-painter behavioural tests); `npm audit --audit-level=high` PASS (0 vulnerabilities).
- `node web/tests/layout_editor.browser.test.js` PASS against the current server build and a disposable app directory. Covers controller outputs, cabinet assignment/order, output path mapping, multi-range drawer allocation, conflict validation, Locate/Stop and lifecycle-preservation cases. WLED endpoints were test-local loopback simulators only; no page errors were reported.
- `WLEDGER_TASK067_LOCATE=1 GO_BIN=<temporary Docker Go shim> node web/tests/task064_fresh_install.browser.test.js` PASS. Covers first install/migrations 1–24, controller output configuration, category/specification/product lifecycle, five-controller Locate/Stop, interrupted activation/Stop recovery, independent backup restore, process fencing and unrelated-pixel preservation. WLED endpoints were test-local loopback simulators only; `findings` and page errors were empty. Browser console entries corresponded to expected invalid/conflict requests and deliberately killed test processes.
- The Go backup, database, storage, hardware, handler, cabinet-pixel and WLED packages all passed in the complete and race-enabled suites. The fresh-install restore archive was version 2.3 and restored outputs, cabinet wiring, allocations and inventory into an independent database.
- `sqlc generate` and `templ generate` were not rerun during this continuation; generated Go/template files compile and are covered by the passing Go suite. The earlier handover checkpoint recorded both generators as passing with no updates.
- No production or staging database, real controller, deployment, coordinate conversion, or hardware operation was used. Hardware commissioning and independent published-source review remain owner-controlled.

## Working-tree changes and exclusions

- Task source/tests/configuration currently modified or added: controller configuration model and tests; migration 024 and SQL queries/generated sqlc code; hardware handlers/service and editor UI; cabinet output projection, path topology, and drawer allocation; WLED client/Locate safety; backup manifest/export/restore compatibility and tests; product-label/scanner/layout generated templates; browser tests; package-lock security updates; `.dockerignore`; and this handover.
- Existing protected working-tree changes are preserved and excluded: `AGENTS.md`, `docs/OMO_HANDOVER.md`, untracked `docs/TASK056_FUNCTIONAL_AUDIT.md`, and `.task055b-staging/`. Do not stage any of them. No credentials or secrets were intentionally added.
- Safety incident: an overly broad search pattern once matched a nested path under `.task055b-staging/build-context/` and returned source snippets. No files in that tree were written or copied by the agent; do not access it again. The `.dockerignore` exclusion is in place.

## Remaining work and publication

- Owner-requested implementation requirements are complete in the reviewed working tree: controller/output settings and visual mapping; cabinet controller assignment separated from output config; multiple drawer pixel ranges and overlap/capacity validation; multi-controller Locate/Stop/Global Off guards and durable recovery; category/specification/product purchasing/unit/CSV workflows; and migration plus backup/restore compatibility.
- The next step is to publish only the reviewed task source/tests/migration/generated files and this active development note to `origin/development/chatgpt-m6-m7`. Protected files and `.task055b-staging/` remain excluded.
- After the implementation commit, record its full SHA and parent SHA here in a documentation-only follow-up commit, then push and stop for the owner's independent GitHub source review.
- Remaining non-code acceptance is owner-controlled hardware commissioning against verified real controller firmware/wiring and subsequent independent published-source review. No physical WLED operation is authorized by this task.

## Owner hardware-acceptance checklist (not performed)

1. Back up the installation database and uploads; confirm the deployed version and migration 024 state. Keep `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false` during all inspection/configuration.
2. Confirm each controller’s exact model/firmware, unique endpoint/port, network reachability, and appropriate timeout. Confirm cabinet-assigned controllers are exclusively assigned and output order matches the actual cabinet wiring.
3. With read-only `/json/info` and `/json/cfg` inspection, compare configured output counts/capacity, GPIOs, WLED segment boundaries, direction/reversal, serpentine dimensions, and any custom pixel permutation to the physical strips. Resolve mismatches before using Locate.
4. Verify cabinet path drawings independently of drawer/bin geometry. For every drawer, enter each non-contiguous pixel range, confirm capacity/conflict previews, and compare its resolved controller-local/segment-local ranges against the physical LED position.
5. Confirm inventory bin/product identities, quantities, and QR labels remain unchanged by hardware or mapping edits. Take another backup and rehearse restore in a disposable copy.
6. Only after the owner explicitly authorizes commissioning, enable output in a controlled maintenance window; test one controller and one drawer at low brightness, verify neighboring/unowned pixels, exercise Stop and Global Off, then test disconnect/reconnect and restart recovery. Re-disable output and verify no active/partial Locate ownership remains.
7. Record actual firmware/controller versions, tested mappings, observed payload/state, recovery result, and any discrepancies before authorizing routine use.
