# WLEDger OmO Development Handover

## Active session and authorization

This is the single active development handover document. `AGENTS.md` defines
development policy; the root `ROADMAP.md` records product requirements and design
updates. Historical handovers are references, not active instructions.

Current task: Task 054B — SQLite Cross-Process Locate Coordination.

Baseline: branch `development/chatgpt-m6-m7` at
`68a3821459e359ec2032fbbfd4e810008f71e496`. Task 054A Approach A is approved
in principle. The owner authorizes Task 054B implementation and publication to
the authorized development branch. Use disposable SQLite databases and local
WLED mocks only; do not contact production systems or physical hardware.

### Task 053 status — implementation verified; publication pending

Test-only coverage now exercises the exact five-controller reference topology
(capacities 200/250/150/300/100) with multiple explicit strings and alternating
serpentine path sections. It checks cabinet ranges 25–34, 195–210, 225–234,
445–460 and 895–910 at controller-local and string-local coordinates; the
browser previews those destinations and five local mock WLED controllers record
and assert the Locate payloads.

File-backed SQLite handler tests submit controller reassignment, wiring and
allocation changes during both durable `active` and unresolved `partial` Locate
sessions. Each HTTP handler rejects the request, and snapshots prove cabinet,
membership, wiring, allocation and session records remain unchanged. Browser
coverage also adds and removes a controller on an existing wired cabinet with a
saved allocation, checking before/after destinations, explicit confirmation,
stale electrical revision rejection and persistent non-mutation.

No production application logic changed and no reproducible application defect
was found. Browser automation used only a disposable application database and
five local mock WLED endpoints; no production database, physical controller or
deployment was accessed.

Verification passed:
- `npm test` (20 JavaScript behavior tests)
- `node --check web/tests/layout_editor.browser.test.js`
- `node --check web/static/js/physical_layout_editor.js`
- `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:layout-browser` (PASS; zero page errors and zero runtime console errors)
- Focused handler and five-controller Locate Go acceptance tests
- `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go test -tags fts5 -count=1 ./...`
- `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go vet -tags fts5 ./...`
- `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go build -tags fts5 ./...`
- `git diff --check`

Changed task files: `internal/handler/layout_active_locate_test.go`,
`internal/wled/physical_locate_reference_config_test.go`, the configurable mock
response in `internal/wled/physical_locate_test.go`,
`web/tests/layout_editor.browser.test.js`, and this handover document. No
acceptance gaps remain within Task 053's requested scenarios; the previously
documented process-local Locate lease limitation remains outside this test task.

Publication verified: test-only task commit
`21052c7b595f237a30e33b30daf186edd7f12e53`, parent
`97cc97b3271c4fe557bd2f03c25039b35a17c64e`; the pushed remote ref
`origin/development/chatgpt-m6-m7` resolves to this commit. This handover
publication record follows in a documentation-only commit. Independent GitHub
source review remains required; stop after publication and do not merge, deploy
or begin another task.

### Task 051 outcome at Task 053 baseline — historical

Task 051 added browser preview/confirmation correction coverage and was published
as `ffcd23c4155a675d605fac3630cf62c30971811e`, parent
`60198881a10ba9be6a066cc4579d6135ada78fda`. Its local/remote publication was
verified and independent GitHub review remained pending. The Task 049 cleanup
outcome and Task 048 architecture sections below are historical evidence and do
not expand Task 053 authorization.

Task 048 currently stores drawer ranges in one controller's local 1-based space,
with controller membership order and a one-controller-per-drawer invariant. This
follow-up supersedes those user-facing and mapping-engine constraints while
preserving stable cabinet/controller/drawer/bin/inventory identities, legacy
electrical containers, and safe read-only WLED discovery. The new canonical
mapping is cabinet-wide 1-based pixels, derived by controller order and string
order; drawer numbering/geometry remains independent.

### Implementation plan (recorded before code changes)

1. **Additive schema and safe migration:** add ordered physical strings per
   cabinet/controller and ordered path sections per string (pixel count plus an
   arbitrary ordered polyline in cabinet coordinates). Keep physical drawer
   geometry and LED wiring paths as separate entities. Make drawer allocation
   `start_pixel`/`pixel_count` canonical cabinet-wide ranges and remove the
   one-controller-per-drawer database rule. In a new migration, convert every
   Task 048 controller-local allocation to cabinet-wide numbering by adding the
   capacities of earlier controllers in saved slot order. Preserve IDs/order/
   timestamps; abort migration if a saved mapping cannot be converted and
   validated unambiguously. Leave bins, attachments, inventory, legacy
   containers, `segment_id`, and `bins.led_index` untouched.
2. **One capacity/coordinate service:** calculate each controller capacity from
   its ordered strings (retain a clearly identified compatibility path for
   pre-existing controllers that have no explicit string definitions); derive
   cabinet offsets and total deterministically. Validate overlaps and bounds in
   cabinet coordinates. Any controller-order or length edit that changes saved
   mappings must show a before/after destination preview and require explicit
   confirmation; configuration writes remain blocked during active Locate.
3. **Safe multi-controller Locate:** intersect cabinet-wide drawer ranges with
   ordered controller spans, translate intersections to controller-local
   1-based pixels, then reuse the existing attested output/segment mapper. Validate
   every affected controller before any write. Persist one logical session plus
   durable per-controller target snapshots; acquire per-controller locks in
   sorted ID order, record attempted activations before requests, and attempt
   targeted cleanup on every controller that may have received a request. Keep
   existing topology fingerprints, restart/timeout recovery, process-local lease,
   and explicit partial/failure reporting; never claim network atomicity or use
   GlobalOff for cleanup.
4. **Backup and restore:** extend current exports with controller order, string
   definitions, path sections, and cabinet-wide allocations. Continue reading
   older v1/v2 archives; convert old controller-local allocation ranges using
   archived membership order/capacity, or reject before mutation when conversion
   is ambiguous. Keep restore serialized against active Locate and preserve the
   existing no-silent-data-loss behavior.
5. **Simplified Hardware/editor workflow:** make Hardware lead with Cabinets and
   the sequence Create Cabinet → Controllers → Strings → Wiring Layout → Drawer
   Layout → Drawer LEDs. Keep legacy controller/container/segment tools available
   but visually distinct as advanced legacy functions. Extend the existing
   graphical editor for ordered multi-section string paths, string/drawer
   selection/highlighting, controller/string boundaries, cabinet pixel labels,
   and a default single start/count allocation with optional additional ranges.
   Do not derive allocations from drawer number; any automatic proposal must be
   previewed and manually correctable.
6. **Regression and gates:** add migration-preservation and failure tests;
   string ordering/continuous offsets and path validation; controller/string
   crossing and non-contiguous allocation tests; reorder/length preview and
   explicit-confirmation tests; multi-controller Locate/Stop, partial cleanup,
   concurrency/restart/timeout and backup compatibility tests; and disposable DB
   plus mock-WLED browser workflows. Run sqlc/templ generation, Go tests/vet/build,
   JavaScript tests/syntax, CSS build, and Chromium browser tests. No hardware or
   production operation is part of verification.

**Open implementation constraint:** WLED physical output definitions, logical
WLED segments, WLEDger physical strings and cabinet-wide pixels remain distinct.
Do not infer a one-to-one output/string/segment relationship. Activation must
continue to verify each controller's live `/json/info`, `/json/cfg` and segment
state against its own safety fingerprint before mapping controller-local pixels.
The process-local Locate lease remains a documented cross-process limitation.

### Follow-up implementation outcome — 2026-10-12

**Status:** Implemented and verified in the local working tree at baseline
`c35b944d73ec9f055fd0e6abcaa04ccb06175b57`. It is intentionally not committed or
pushed: the owner explicitly requested an owner-reviewable working tree. The
remote development branch still points to the baseline. `AGENTS.md` is untouched
by this work and remains the sole unrelated unstaged modification.

- Migration `019_cabinet_wiring_global_pixels.sql` adds ordered controller
  strings and ordered cabinet-coordinate path sections; translates Task 048
  controller-local allocations by controller slot and legacy capacity; rebuilds
  allocations into cabinet-wide ranges; allows a range to cross controller
  boundaries; and extends durable Locate sessions with per-controller cleanup
  targets. It refuses ambiguous/out-of-capacity legacy ranges before changing
  connection/schema state. Controller slots above 4 are allowed only while an
  in-transaction reorder guard is present.
- `internal/cabinetpixels` is the shared deterministic capacity/span translator.
  Explicit strings determine controller capacity; pre-existing controllers with
  no string configuration use their preserved legacy capacity until explicitly
  configured. String names/orders, pixel counts, completion state and arbitrary
  ordered polylines are persisted independently of drawer geometry and inventory.
- Drawer allocation inputs and persisted rows now use cabinet-wide 1-based
  starts/counts. Database/service validation rejects global overlap and capacity
  overflow. Controller order and string-length/path edits compare old/new
  destinations and require a confirmation when saved drawer pixels are affected.
- Locate splits global ranges across ordered controllers and strings, validates
  every affected controller and WLED topology before activation, locks targets in
  ascending controller-ID order, persists one logical session and each local
  cleanup snapshot, and retries targeted cleanup for every attempted target on
  partial failure. Durable session records block backup restore while any target
  may still own pixels. No global-off command or real controller was used.
- Current backups now include controller ordering, strings, sections and
  cabinet-wide allocations. A Task 048 archive containing controller-local
  ranges is decoded into cabinet-wide coordinates from its saved membership
  order/capacities. Missing wiring data cannot silently erase configured wiring;
  ambiguous legacy coordinate conversion is rejected before restore mutation.
- Hardware now leads with Cabinets and an ordered setup flow. The existing editor
  draws ordered string polylines over the same cabinet-coordinate canvas, shows
  string/controller pixel ranges and drawer highlights, offers simple start/count
  allocation with advanced additional ranges, and keeps read-only WLED output
  diagnostics in the admin-only advanced section. Legacy containers/segments stay
  separate and preserved. Automatic drawer-to-pixel inference remains optional
  and was not implemented; manual mapping remains available.

**Verification completed:**
- `sqlc generate` and `templ generate` completed successfully.
- `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go test -tags fts5 -count=1 ./...` passed.
- `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go vet -tags fts5 ./...` passed.
- `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go build -tags fts5 ./...` passed.
- `npm test` passed all 20 existing JavaScript behavior tests; `node --check`
  passed for the editor and browser test.
- `npm run build:css:prod` passed with the existing nonfatal daisyUI
  `@property --radialprogress` optimizer warning.
- `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:layout-browser`
  passed using a disposable SQLite DB and local mock WLED server. It verifies
  cabinet-led workflow, five-controller cap/order, four-section serpentine path,
  explicit mapping-impact confirmation, cross-string/cross-controller ranges,
  read-only output discovery, Locate/Stop and inventory preservation; zero page
  errors and zero runtime console errors (one expected HTTP 409 warning).
- `git diff --check` passed. No production database, deployment, merge, commit,
  push, or physical WLED controller was used.

**Changed task files (41; `AGENTS.md` excluded):**
- Product/handover: `ROADMAP.md`, `docs/OMO_HANDOVER.md`.
- Migration/queries/generated DB: `sql/schema/019_cabinet_wiring_global_pixels.sql`,
  `sql/queries/drawer_led_allocations.sql`, `sql/queries/physical_storage.sql`,
  `internal/db/db.go`, `internal/db/models.go`,
  `internal/db/drawer_led_allocations.sql.go`,
  `internal/db/physical_storage.sql.go`, `internal/db/querier.go`.
- Pixel mapping/storage: `internal/cabinetpixels/ranges.go`,
  `internal/cabinetpixels/topology.go`, `internal/cabinetpixels/ranges_test.go`,
  `internal/storage/cabinet_controller.go`,
  `internal/storage/controller_capacity.go`, `internal/storage/layout.go`,
  `internal/storage/led_allocations.go`, `internal/storage/cabinet_wiring.go`,
  `internal/storage/led_allocations_test.go`, `internal/storage/cabinet_wiring_test.go`.
- Locate/backup/handlers: `internal/wled/physical_locate.go`,
  `internal/wled/physical_locate_test.go`,
  `internal/wled/physical_locate_multicontroller_test.go`,
  `internal/backup/service.go`, `internal/backup/storage_manifest.go`,
  `internal/backup/storage_restore.go`,
  `internal/backup/cabinet_controllers_backup_test.go`,
  `internal/backup/physical_roundtrip_test.go`,
  `internal/backup/cabinet_wiring_backup_test.go`,
  `internal/handler/hardware.go`, `internal/handler/layout_editor.go`,
  `internal/router/router.go`,
  `internal/db/cabinet_controllers_migration_test.go`,
  `internal/db/cabinet_wiring_migration_test.go`.
- UI/assets/browser regression: `web/pages/hardware.templ`,
  `web/pages/hardware_templ.go`, `web/pages/storage_layout_editor.templ`,
  `web/pages/storage_layout_editor_templ.go`,
  `web/static/js/physical_layout_editor.js`, `web/static/css/output.css`,
  `web/tests/layout_editor.browser.test.js`.

**Remaining risks/limits:** The Locate write lease is process-local; other
WLEDger processes or external WLED writers can interfere. WLEDger strings are
not inferred from WLED outputs, and no 1:1 string/output/segment relation is
claimed. Physical path overlays interpolate pixels along drawn polylines for
visual inspection; they do not configure LEDs or transform electrical mapping.
Legacy installations with invalid controller-local ranges must resolve the
reported migration preflight issue before upgrading. `AGENTS.md` remains
modified/unstaged exactly as it was at task start; it is not in this task's diff.

## Task 048 — Multi-Controller Cabinet Architecture — 2026-10-12

- **Migration `018_cabinet_controllers.sql`:** creates `cabinet_controllers` with
  ordered slots 0–4, a unique controller owner, and foreign keys to existing
  cabinets/controllers. It backfills exactly one slot-0 membership for each
  existing non-NULL `cabinets.controller_id`; NULL/unassigned cabinets remain
  unassigned, and the legacy primary pointer remains unchanged. It rebuilds only
  `drawer_led_allocations` to reference the membership pair, copying allocation
  IDs, controller/cabinet/drawer IDs, 1-based starts, counts, display order,
  timestamps and AUTOINCREMENT high-water mark. It does not modify drawers,
  attachments, bins, inventory, legacy containers or bin LED indices. Database
  constraints also reject more than five controllers, shared controller
  ownership, multiple controllers on one drawer, and overlapping ranges within a
  controller. Down refuses to discard secondary memberships or any saved ranges.
- **Designer workflow:** admins check up to five cabinet controllers and save
  the assignment separately; each controller has independent capacity. In the
  drawer properties panel, select one assigned controller, add one or more
  disjoint ranges, and save the controller ID with the mapping. Same pixel
  numbers may be reused on another controller. Clearing a drawer's existing
  ranges is required before changing that drawer's controller. Physical layout
  and electrical saves remain separate and do not activate hardware.
- **Output discovery/visualization:** explicit user action GETs only `/json/info`
  and `/json/cfg` for a controller assigned to that cabinet. It shows output
  list order, explicit controller-wide start, length, active GPIO pins and
  corresponding 1-based inclusive LED numbers, plus range overlays on the
  physical output bar. Missing/gapped starts, unsupported pins or unsafe
  topology fail closed. Discovery does not GET `/json/state`, write WLED, or
  equate a physical output with a WLED segment.
- **Locate/safety:** drawer Locate validates every saved range has the same
  assigned controller and capacity, maps only within that controller's attested
  WLED segment topology, and persists the selected controller and ranges in its
  durable session. Drawer Stop resolves the active durable session by drawer and
  stops against the controller captured when it started, not a subsequently
  changed cabinet primary pointer. Capacity, assignment, allocation and backup
  restore writes serialize against the process-wide physical Locate coordinator
  and are refused while any session may still own pixels. Mapping attestation
  remains per-controller and existing topology fingerprint checks remain
  fail-closed.
- **Backup/compatibility:** new current-format exports declare exact controller
  memberships. Old current-format archives without that additive list derive
  only their legacy `cabinets.controller_id` membership at slot 0. Restore over
  an installation with secondary memberships is refused rather than erasing
  those assignments. Current archives validate controller exclusivity, slot
  range, allocation ownership/capacity/overlap, and one controller per drawer.
- **Verification:** `sqlc generate`, `templ generate`, `npm run build:css:prod`,
  `npm test` (20 tests), JavaScript syntax checks, `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:layout-browser`,
  `CGO_ENABLED=1 go test -tags fts5 -count=1 ./...`, `CGO_ENABLED=1 go vet
  -tags fts5 ./...`, `CGO_ENABLED=1 go build -tags fts5 ./...`, and
  `git diff --check` passed. The browser test uses a disposable SQLite database
  and a local mock WLED HTTP server, verifies five assignments and the sixth
  controller cap, separate capacity saves, cross-output read-only discovery,
  same-index allocation on two controller IDs, controller-specific overlap
  rejection, and Locate/Stop plus a secondary-controller allocation. It reports
  no page/runtime errors and one expected warning for the deliberately exercised
  HTTP 409. The CSS optimizer reports the existing nonfatal daisyUI
  `@property --radialprogress` warning. The Go tools were invoked from
  `/home/spetchal/gotoolchain/go/bin` because Go is not on the shell `PATH`.
  Migration and backup tests use disposable SQLite databases; Locate and output
  discovery tests use mock HTTP servers. No real WLED or production operation
  was performed.
- **Review:** inspected migration up/down and FK/trigger contracts, allocation
  service/validation, secondary-controller Locate selection and durable Stop,
  output discovery's GET-only route, old/current backup handling and active
  session configuration-write serialization. The regression suite includes
  single-controller preservation, five-controller/exclusive membership rules,
  per-controller numbering/overlap/capacity, and secondary controller routing.
- **Publication authorization:** the owner explicitly authorized commit/push
  only to `origin/development/chatgpt-m6-m7`, superseding the previous Task 048
  clean-working-tree requirement. Publication requires exactly these 31 verified
  Task 048 files staged, with `AGENTS.md` excluded and preserved unchanged;
  unrelated working-tree changes may remain unstaged. No merge, deployment,
  production operation, real WLED contact, or push to `main` is authorized.
- **Publication status:** published as
  `c35b944d73ec9f055fd0e6abcaa04ccb06175b57` to
  `origin/development/chatgpt-m6-m7`; parent is
  `c9c64aa716a6c40d6f7ae9f7876b5b67ca56b4ed`. The simplified cabinet mapping
  follow-up is a separate uncommitted owner-requested task; it is not authorized
  for commit or push.
- **Remaining limitations:** discovered output geometry is not persisted and
  hardware support is limited to configurations accepted by the current safe
  linear mapping rules. No real WLED topology was inspected. The Locate write
  lease remains process-local and external WLED writers can interfere; only one
  active physical session is permitted globally.

## Task 046 — Permanent Drawer LED Allocation — 2026-10-12

## Task 046 — Permanent Drawer LED Allocation — 2026-10-12

- **Baseline/branch:** started clean on `development/chatgpt-m6-m7` at
  `c65240efe4bef607d4acc5d1d0f54126f65d71c7` (parent of the task commit).
  Owner authorized this extension and only a development-branch commit/push.
- **Audit conclusion:** `controllers.led_count` is the existing controller
  capacity. Legacy `containers.led_start/led_count` are stored on electrical
  container/WLED-segment rows; `segment_id` is not a physical string/output
  identifier, and neither these tables nor `bins.led_index` defines a mapping
  from physical output order to WLED segments. `drawers` are independent
  physical records. No legacy mapping was converted or changed.
- **Addressing contract:** new drawer allocations use controller-wide,
  user-facing 1-based pixel numbers `[start, start + count - 1]`, bounded by
  that controller's positive `controllers.led_count`. This namespace is
  independent of physical-string and WLED-segment boundaries. For a controller
  with two 100-pixel strings, Start 97 / Count 8 is one allocation 97–104; it
  is not split. This does not infer which WLED segment/output receives an index.
- **Persistence:** additive migration 015 creates `drawer_led_allocations` with
  stable row ID, drawer/cabinet/controller FKs, 1-based start, count and order.
  Composite FKs require the allocation's cabinet to own the drawer and assigned
  controller, protecting controller reassignment. Saves use `BEGIN IMMEDIATE`,
  `cabinets.electrical_revision` optimistic concurrency, range/overflow/capacity
  checks and controller-wide overlap rejection. Multiple ordered, nonoverlapping
  ranges per drawer are supported. An admin can assign/unassign a controller
  in the Designer when no drawer ranges exist; assignment changes use the cabinet
  electrical revision and preserve all physical/inventory records. An admin can
  explicitly set total `controllers.led_count` from the Designer when missing or
  incorrect; capacity edits use expected-value and cabinet electrical-revision
  checks and cannot shrink below any saved allocation.
- **UI/security:** the Designer loads server allocations, shows Start/Count/End,
  assigned controller and validation status, plus add/remove controls. “Save
  layout”, “Save controller assignment”, “Save controller capacity” and “Save
  LED mapping” are separate POST transactions. A new drawer must first be
  persisted for its stable ID; unsaved canvas edits block navigation-causing
  electrical saves, and unsaved electrical drafts block physical-layout saves.
  Controller assignment/capacity writes are admin-authorized;
  drawer mapping/layout writes remain editor/admin-authorized; every new write
  is CSRF protected. Legacy browser-local LED drafts are never imported or
  removed; the UI warns and leaves them untouched. Mapping/config writes issue
  no WLED requests.
- **Backup/lifecycle:** new exports include allocations and restore validates
  identities, controller ownership, capacity, ordering, overflow and overlap.
  Old current-format archives lacking the new allocation list remain readable,
  but restoring one over existing allocations is refused to prevent data loss.
  Drawer archive preserves mappings; permanent deletion is blocked until ranges
  are explicitly cleared. Legacy bin/stock/container rows remain untouched.
- **Related defect fixed:** controller online-status refresh previously used an
  update query that also wrote a zero `led_count`; status refresh now updates
  online state only, preserving configured allocation capacity.
- **Delegation/review:** DeepSeek V4.1 Flash performed a read-only source audit
  and a follow-up review; its capacity-source and unsaved-edit findings were
  resolved and rechecked. Implementation and acceptance remain the lead agent's
  responsibility.
- **Verification:** `sqlc generate`, `templ generate`, production CSS build,
  `npm test`, headless Chromium `npm run test:layout-browser`, focused and full
  `CGO_ENABLED=1 go test -tags fts5`, `go vet`, `go build`, and `git diff
  --check` pass. The Chromium run assigned a controller through the UI, saved a
  97–104 cross-boundary range, reloaded two disjoint ranges, checked capacity/
  overlap errors, saved controller capacity through the UI, proved unsaved canvas
  edits block electrical-save navigation, and observed zero WLED requests and zero
  page/console errors. Go router tests also cover authorization and CSRF rejection
  for these configuration write routes. CSS
  retains the existing nonfatal daisyUI `@property --radialprogress` warning.
  Full commit/parent hashes, changed files and remote synchronization are reported
  in the task completion report after publication.
- **Risks/limits:** no physical string/output configuration table, WLED segment
  coordinate conversion, Locate consumption or hardware activation was added.
  A separately reviewed future Locate path must explicitly convert saved
  controller-wide pixels to the intended WLED segment/output without treating
  legacy `containers` as drawers. New v2.0 backups include an additive allocation
  field unknown to older binaries; restoring new exports requires this or a newer
  build. The new build accepts old backups but refuses old snapshots over saved
  drawer allocations so they cannot silently erase unmappable data.

## Task 047 — Drawer LED Locate and Hardware Control — 2026-10-12

- **Git baseline:** `development/chatgpt-m6-m7` at
  `8e078fa509bb960b5cd80285cc614e0a9ba4ddad`; starting tree was clean.
  User authorized implementation/testing and commit/push only to this branch;
  real WLED requests, production LED activation, deployment, production DB
  changes, and merging to main are prohibited.
- **Resolution:** a selected inventory part/bin is validated against the part's
  current assignment, then resolved through `bin_drawer_attachments` to the
  physical drawer, cabinet controller and saved `drawer_led_allocations` inside
  a consistent DB snapshot. Legacy `containers`, `segment_id`, and
  `bins.led_index` are not used for this path.
- **Safety/configuration:** migration 016 adds an admin-only, default-disabled
  controller mapping/ownership attestation. Both output-order verification and an
  explicit reservation of saved drawer ranges for exclusive WLEDger Locate use
  are required. The admin confirmation reads `/json/info` and
  `/json/cfg` and persists a fingerprint of controller identity, explicit
  contiguous output starts, lengths, types, pins/order, and supported linear
  mapping settings. Every Locate re-reads `/json/info`, `/json/cfg`, and
  `/json/state` and refuses a changed fingerprint before writes; capacity/MAC,
  matrix/custom maps, segment offsets/coverage/IDs, and target segment mode are
  checked. WLED configurations that omit explicit start/pin geometry remain
  disabled rather than receiving an inferred mapping. Only gap-free segment
  geometry covering the complete configured controller is translated.
  Unsupported RGBW, matrix/custom maps, reversed or skipped outputs,
  grouping/spacing/reversal, active effects, missing state, or stale configuration
  fail without a pixel write.
- **WLED command contract:** 1-based controller ranges are converted to
  zero-based global intervals, intersected with WLED segment `[start, stop)`
  intervals, then emitted as one bounded `i` command per intersection. Example
  for range 97–104 across segment 10 `[0,100)` and segment 20 `[100,200)`:
  `POST /json/state {"seg":[{"id":10,"i":[96,100,[R,G,B]]}]}` and
  `POST /json/state {"seg":[{"id":20,"i":[0,4,[R,G,B]]}]}`. No global power,
  segment definitions, effects, or full pixel arrays are written. Stop sends
  black only over explicitly reserved app-owned ranges and reports completion
  only after positive WLED acknowledgements; it does not restore previous
  per-pixel overrides or claim the whole controller is off.
- **Legacy hardware arbitration:** a process-wide write lease plus controller
  identity/endpoint gates are shared across separate `Client` instances. A
  durable physical session reserves the app-wide lease from activation through
  confirmed Stop/failure cleanup, including interrupted-session recovery. While
  any physical session is active, legacy LocatePart/LocateBin/LocateDrawer,
  FlashError, GlobalOff, generic JSON state writes and delayed legacy Locate
  auto-off are serialized/rejected with `ErrHardwareBusy` rather than able to
  overwrite the session, including through another configured endpoint in this
  process. The lease mutex spans the complete request/flash sequence, so legacy
  writes across different controllers serialize and a slow request can delay a
  physical Locate start or another legacy write. FlashError performs its blink
  synchronously and reports request failures. GlobalOff remains an explicit
  controller-wide `on:false` power-off:
  it cannot issue any write while a physical session is active, can affect
  unrelated LED activity when no session is active, and returns aggregated
  failures truthfully if one or more controller commands fail. If a physical
  session starts during a multi-controller GlobalOff run, earlier controllers
  may already have been powered off before later requests are rejected.
- **Session/cleanup:** session ownership, selected drawer/bin/controller,
  allocated ranges, color/brightness, topology fingerprint, last successful
  operation, state and error are persisted. One active app session is enforced;
  Locate switches only after cleanup of the previous highlight. The two-minute
  timeout, restart recovery and bounded HTTP failures use safe range-off attempts;
  configuration mismatch or missing confirmation leaves an explicit `partial`
  state and withholds unsafe cleanup writes.
- **UI/security:** part-detail Locate is per inventory row/bin; it redirects to
  the physical drawer view with the selected bin layout opened and highlighted.
  Drawer layout also has per-bin Locate and owner-only Stop/Turn Off controls.
  Hardware action routes use editor/admin authorization plus CSRF; the mapping
  attestation route is admin-only plus CSRF.
- **Tests/acceptance:** mock-only WLED tests cover the 97–104 split, disjoint
  ranges, exact segment-relative `i` payloads and exclusive stops, segment
  protection, multiple locations, missing physical association, mapping
  attestation/fingerprint changes, missing output geometry, offline controller,
  active effects/segment offsets, partial failures, damaged recovery commands,
  repeat Stop, timeout, drawer switching, concurrency, restart recovery, and
  legacy Locate/FlashError/GlobalOff/raw-state conflicts across independent
  `Client` instances. Go 1.25.5 was found and verified at both
  `/home/spetchal/gotoolchain/go/bin/go` and
  `/home/spetchal/.local/go-sdk/go/bin/go`; both report `go1.25.5 linux/amd64`.
  Full `CGO_ENABLED=1 go test -tags fts5 -count=1 ./...`, `go vet`, and `go
  build` pass. `npm test` passes all 20 JS behavior tests. The full Chromium
  workflow passes against the disposable database and mock controller: regular
  editing made zero WLED requests; part Locate selected bin 99001 in drawer 1
  and bin 99005 in drawer 2, verified the exact cross-segment allocations,
  highlighted each bin, stopped both ranges, and displayed the missing physical
  association error without any WLED write. It reported no page/runtime errors;
  one expected browser warning came from that intentional HTTP 409. `sqlc
  generate`, `templ generate`, and `git diff --check` pass. No production
  controller was contacted.
- **Publication:** implementation commit
  `420f7970816469b2a3de86284ae16b84708782ea` (parent
  `8e078fa509bb960b5cd80285cc614e0a9ba4ddad`) is pushed to
  `origin/development/chatgpt-m6-m7` as a fast-forward. It changes 41 files; the
  complete path/stat list is in that commit. Tests were rerun and passed before
  publication. No hardware or production operation was performed.
- **Limitations:** no real controller topology was inspected; activation remains
  disabled per controller until an administrator explicitly attests it and live
  read-only checks continue to match. The hardware gate is process-local, not a
  multi-process lock: run one WLEDger hardware-control process per controller/
  database while physical Locate is enabled. Another process or external WLED
  writer can still interfere; cross-process/external arbitration remains a
  separate architectural task. A stale/offline `partial` session retains the
  process-wide lease and currently retries cleanup every 15 seconds without a
  retry cap, so legacy hardware writes remain unavailable until cleanup is
  confirmed or the stale session is explicitly resolved. Stop blackens only
  app-owned ranges and does not restore preexisting per-pixel state; a blocked
  legacy auto-off can therefore leave its prior unrelated legacy indication on.
  GlobalOff is deliberately a device-wide power-off when no physical session
  is active and may affect unrelated LED activity; while a session is active it
  is rejected before any write. A session beginning mid-run can cause a partial
  off across controllers, which is reported truthfully.

## M7 LED sidebar authorization boundary — 2026-10-12

- Source audit: migration 010 stores `led_start`/`led_count` on `containers`.
  The active architecture and prior authorization classify containers as
  electrical strings/segments, not physical drawer records. Existing hardware
  allocation services validate/update those electrical rows. `ROADMAP.md`
  describes drawer-to-segment `drawer_led_range` conceptually, but no such
  physical drawer relationship exists in the current schema. M7 authorization
  requires physical edits not to modify electrical mappings; M8/electrical
  topology remains unauthorized. Therefore this task does not write the legacy
  fields or invent a physical drawer-to-segment mapping.
- The selected-drawer sidebar displays numeric LED Start and LED Count inputs,
  plus the cabinet's assigned controller/total LED capacity read-only. Values
  are explicitly labelled **browser-local draft only** and persist under a
  cabinet/drawer-stable-key localStorage record after “Save LED draft”. They are
  never included in the physical layout POST, never written to SQLite, and do
  not activate WLED. New drawers must first be saved to obtain a stable ID.
- Draft validation rejects partial pairs, negative/fractional starts, nonpositive
  or fractional counts, integer overflow, controller-capacity overflow and
  overlapping draft ranges. Because segment identity is not associated with
  physical drawers, overlap validation is intentionally conservative and
  cabinet-wide; UI text identifies the missing segment mapping. With no
  controller, the draft remains local and reports that range validation is
  incomplete. Broken controller foreign-key references are shown as errors.
- Browser coverage verifies blank defaults, negative/out-of-range/overlap
  feedback, valid draft local save/reload, unchanged physical save payload and
  unchanged electrical container `(id, controller_id, led_start, led_count)`.
  This keeps M7 safe; an authorized electrical mapping workflow is still needed
  before any draft can become a live allocation.

## LED Start/Count authorization and draft workflow — 2026-10-12

- Reviewed migration 010, `internal/hardware/allocation.go`, controller and
  container queries, backup validation/restore and current roadmap architecture.
  Existing `led_start`/`led_count` fields and validators describe electrical
  container/segment allocations; `containers` are not physical drawers. The
  roadmap's `drawer_led_range` is a future conceptual model, not implemented
  persistence. M7 physical layout authorization requires not changing electrical
  mappings; M8/electrical topology remains unauthorized. Therefore no writer to
  `containers.led_start` or `containers.led_count` was added or invoked.
- Drawer properties now show numeric LED Start/Count and the cabinet's
  controller name, ID and total LED count read-only. Values are explicitly
  labelled browser-local drafts and are stored in localStorage only after
  **Save LED draft**. They are not included in the physical layout POST and do
  not persist to SQLite, backups, hardware, or active mappings. A newly drawn
  drawer must first be physically saved to acquire a stable drawer ID.
- Draft checks reject partial pairs, negative/fractional starts, nonpositive or
  fractional counts, overflow, controller-total overflow, missing controller
  references, and overlapping drafts. Without a controller, the UI explains
  that the range cannot be bounded. Without a drawer-to-segment identity, overlap
  checks are conservative across the cabinet and the UI explicitly says the
  segment is not assigned; draft values cannot be promoted to live allocations
  without separate authorization and a reviewed mapping workflow.
- Browser test coverage verifies blank defaults; negative, out-of-range and
  overlap errors; save/reload of browser-local drafts; absence of LED fields in
  the physical layout request; and unchanged controller/container allocation
  rows. A deliberately corrupted controller foreign key is reported inline.

## M7 Visual Cabinet Designer — 2026-10-11

- Replaced the primary form-list workflow in `StorageLayoutEditor` with a
  responsive three-panel designer: drawing tools, an interactive center SVG
  canvas, and contextual numeric properties. Drawer and bin rectangles support
  pointer selection, drag movement, four visible resize handles, zoom, pan,
  optional grid, adjustable spacing, snap toggle, undo/redo, arrow-key movement,
  and Delete/Backspace confirmation. Drawer double-click enters the corresponding
  local-coordinate bin canvas; bin drawing associates only a user-selected
  existing unattached inventory bin.
- Added Paint tools for a cabinet outline, decorative rectangles, lines and
  labels. Paint objects use a separate `decorations` payload and are never
  drawers, bins, stock or LED records. Migration `014_cabinet_decorations.sql`
  stores them in additive cabinet JSON metadata. Save/load and backup restore
  preserve decorations; older backup manifests safely default to an empty list.
- Moved SVG drawing/rendering into `web/static/js/physical_layout_editor.js`.
  SVG template cloning was avoided; the script creates SVG-namespace elements
  with `textContent`, preserving escaping. `WLEDGER_PORT` continues to default
  to 8080 and permits the browser harness to use a fresh random local port.
- The browser suite now drives actual mouse drags against a newly built
  disposable app/database. It checks drawer draw/select/move/resize, exact
  numeric editing, configurable grid spacing, snap on/off, grid visibility,
  zoom/pan, 390px responsive layout, paint object creation plus keyboard
  delete/undo/redo, live cabinet/drawer `viewBox` sizing, overlap and out-of-
  bounds rejection while retaining the draft, bin drawing/move/resize/undo/
  redo/save/reload, safe removal, stock/legacy bin mapping preservation,
  cabinet/drawer deletion and archive. Latest Chromium run: PASS; zero page or
  runtime console errors and zero browser warnings.
- Verification commands: `templ generate`, `sqlc generate`,
  `npm run build:css:prod`, `npm test`, `npm run test:layout-browser`,
  `CGO_ENABLED=1 go test -tags fts5 -count=1 ./...`,
  `CGO_ENABLED=1 go vet -tags fts5 ./...`, `CGO_ENABLED=1 go build -tags fts5
  ./...`, and `git diff --check`.

## Browser-confirmed Add Drawer / Save Layout correction — 2026-10-11

- Headless Chromium reproduction against an isolated SQLite database exposed
  `Cannot read properties of null (reading 'map')` during Alpine initialization.
  Empty `ListDrawersByCabinetIncludingArchived` and attachment queries returned
  nil slices; Go JSON encoded those as `null`, while the component calls `.map()`.
  Initialization failure left the hidden `layout` input blank, and the handler
  consequently rejected the POST as `invalid layout data`.
- `LayoutService.Get` now initializes `Drawers` and `UnattachedBins` as empty
  arrays and normalizes each drawer's `Bins` slice. Add Drawer and Save Layout
  then initialize and persist correctly. Invalid JSON and layout-save errors
  redirect to the editor with a readable `layout_error` rather than rendering a
  raw HTTP error page.
- Added `web/tests/layout_editor.browser.test.js` and Playwright as a dev-only
  dependency. `npm run test:layout-browser` builds and launches the app against
  a fresh temporary directory/database, chooses an available local port through
  `WLEDGER_PORT`, creates a test admin, and seeds one synthetic bin+part. It
  drives the actual Alpine UI and inspects the real POST body/CSRF values. The
  observed browser run passed cabinet create; Add Drawer; drawer edit; valid
  JSON submission; save/reload; cabinet grid dimensions/styles; Grid/List;
  View Bin Layout; explicit bin attach/detach; malformed JSON and overlap
  feedback; blocked cabinet deletion; successful empty drawer/cabinet deletion;
  drawer/cabinet archive; and numeric lifecycle URLs. No JS page errors or
  runtime console errors occurred. One Chromium-generated warning about a
  blocked `beforeunload` prompt occurred during automated navigation while
  unsaved test edits were present; it is filtered separately from runtime errors.
- Empty-cabinet and lifecycle/storage unit tests additionally verify non-nil
  JSON collections, out-of-bounds rejection, rollback, stable identities,
  stale revisions and inventory preservation. No production database or LED
  mapping was accessed.
- The app accepts optional `WLEDGER_PORT`; its default remains `8080`. The
  browser harness uses a disposable random port to avoid interacting with any
  pre-existing server.

## Development-branch M6 presentation checkpoint — 2026-10-11

- Verified origin is `https://github.com/magicaussie/wledger`; initial checkout
  was clean on `transition/omo`. Fetched origin, verified the requested branch
  was also at `771849aafe511d0dc74868878dccfc367e6060af`, then switched to a
  local tracking branch `development/chatgpt-m6-m7`. No divergence was found.
- Source inspection identified missing Grid/List and View Bin Layout controls,
  despite the earlier broad M6 progress descriptions. Added both to the drawer
  page. Inventory cards retain exact per-bin quantities, stable part links and
  bin-filter links; images are optional and broken images hide without hiding
  inventory. Bin layout uses only attachment geometry relative to its drawer.
- Added four presentation strings in all eight locale files. Added a handler
  regression covering multiple part types in a bin, a part in multiple bins,
  zero quantities, navigation links and escaped labels. Updated generated templ
  and minified CSS. No schema, write handlers, stock or electrical changes.
- Verification: `templ generate`, `npm run build:css:prod`, `npm test` (20
  behavioural tests), `go test -tags fts5 -count=1 ./...`,
  `go vet -tags fts5 ./...`, `go build -tags fts5 ./...` and
  `git diff --check` passed. Go checks use the installed Go 1.25.5 SDK with
  CGO enabled. CSS reports the existing nonfatal daisyUI `@property
  --radialprogress` optimization warning. SQLite tests use isolated fixtures.
- Browser verification is NOT complete. `takeScreenshot` failed with
  `Failed to discover browser connector server. Please ensure it's running.`
  Node cannot resolve `playwright` in this checkout. No fresh screenshots,
  interactive tests, mobile checks or measured contrast acceptance were
  obtained. Prior REVISE captures remain historical, not acceptance evidence.
- This is a partial implementation checkpoint, NOT M6 completion. Remaining
  work includes fresh interactive/visual validation of the new presentation
  and the existing picker fixes, cabinet physical-layout presentation, and a
  complete requirements audit. M7 persistence/editor work has not started.
  Do not treat the passing baseline tests as proof that M6 or M7 is complete.
- No delegation, production access, server startup, hardware activation,
  deployment, merge or changes to the archival checkout occurred.

## M6/M7 implementation — 2026-10-11

- M6 cabinet detail now renders its persisted physical drawer geometry as a
  responsive proportional layout, with the existing drawer cards retained as a
  keyboard-accessible alternative. Editors/admins receive an Edit layout link.
- M7 adds migration 013 for explicit cabinet layout bounds (`layout_width`,
  `layout_height`, defaulting existing cabinets to 100×100). It does not alter
  bins, stock, containers, controllers, LEDs or assignments. Backups include
  the additive fields; restoring a pre-M7 snapshot safely supplies the neutral
  100×100 defaults.
- `storage.LayoutService` performs create/read/save of physical layouts inside
  `BEGIN IMMEDIATE` transactions. Save validates finite positive dimensions,
  non-negative origins, cabinet/drawer bounds, drawer/bin overlap, duplicate
  IDs and labels. It uses `layout_revision` for stale editor conflict rejection.
  It preserves cabinet/drawer/bin IDs, stock and assignments. Existing attached
  bins may be moved/resized within their drawer but may not be silently moved
  between drawers. Omitted existing drawers are rejected rather than deleted.
  Only explicitly selected unattached bins can be newly associated.
- Added editor routes restricted to editor/admin and CSRF-protected write
  routes. The responsive editor supports cabinet settings, drawer add/rename/
  order/move/resize, numeric bin geometry and explicit unattached-bin
  association. It includes a browser unsaved-change warning and cancel action.
  It makes no hardware requests and exposes no electrical controls.
- Automated evidence: `sqlc generate`, `templ generate`, `npm run
  build:css:prod`, `npm test`, `go test -tags fts5 -count=1 ./...` all passed.
  `internal/storage/layout_test.go` covers stable IDs/revision, stale conflict,
  overlap rollback and unattached-bin association. CSS retains the known
  nonfatal daisyUI `@property --radialprogress` warning.
- Browser acceptance remains unavailable: the Zed browser connector is not
  running and Playwright is not installed in the checkout. No real browser
  screenshots or pointer/touch test run has been claimed. This is the only
  outstanding verification category; all code paths were verified with
  isolated SQLite fixtures and application test suites.

## Independent M6/M7 verification follow-up — 2026-10-11

- Verified branch `development/chatgpt-m6-m7` at
  `2191fbffcbfb90a24dd042c35e9bb4788b9efc5f`, clean and synchronized with
  `origin/development/chatgpt-m6-m7` before this review.
- Independent persistence review found that an edited drawer could omit an
  existing attached bin. Because omitted attachments were retained, a drawer
  resize could leave a bin outside its saved bounds. Fixed this by requiring
  every existing attachment to be present in the submitted layout. Omission is
  now rejected with `ErrUnsafeLayout`, transactionally preserving the prior
  geometry. Added regression coverage.
- Verified full test, vet, build, templ generation, CSS build and JS suite
  after the correction. A disposable startup applied migrations 001–013 to a
  fresh database successfully. It then could not bind `:8080` because another
  local process already owns the port; no production resource was accessed.
- Zed browser tooling still reports no connector server. `playwright` cannot
  be resolved from this checkout. Browser interaction/visual acceptance is
  therefore NOT TESTED and must be completed manually or in a browser-enabled
  environment. Do not report it as passed.

## M7 full physical-layout lifecycle — 2026-10-11

- Added explicit CSRF-protected editor/admin lifecycle routes and confirmed
  action controls: archive/delete cabinet, archive/delete drawer, and Remove
  placement. Controls require a browser confirmation before posting the current
  layout revision.
- Cabinet archive preserves drawers, bins, stock and electrical records.
  Cabinet deletion is allowed only with zero drawers (active or archived).
  Drawer archive preserves placements and stock. Drawer deletion requires zero
  attached placements. Placement removal deletes only
  `bin_drawer_attachments`; the inventory bin and any part assignments,
  quantities, legacy container mapping and LED fields remain intact.
- Every lifecycle operation executes in `BEGIN IMMEDIATE`, validates the
  cabinet revision, verifies ownership and advances the revision atomically.
  Stale requests, cross-cabinet operations and unsafe deletes reject without
  partial mutation.
- Added lifecycle regression coverage for blocked occupied-drawer deletion,
  placement detachment with inventory-bin preservation, successful empty
  drawer/cabinet deletion, archived drawer state and stale archive rejection.
  Full tagged Go suite, vet, build, templ/sqlc generation, production CSS build
  and JS tests passed after the change. Browser acceptance remains untested
  because the browser connector/Playwright are unavailable.

## M7 browser-reported undefined lifecycle URL correction — 2026-10-11

- Real Chrome acceptance found cabinet deletion posting to
  `/storage/cabinets/undefined/delete`. Root cause: `templ.JSONScript` honors
  `db` JSON tags (`id`, `layout_revision`, `bin_id`), while the editor JS was
  incorrectly reading Go export names (`ID`, `LayoutRevision`, `BinID`). Thus
  the editor state never received a cabinet ID. The same defect affected drawer
  and bin lifecycle URLs and initial editor geometry hydration.
- Corrected every JSON access to the serialized lower-case contract. Lifecycle
  form submission now builds `/storage/cabinets/<stable numeric id>/...` with
  POST, the request-scoped CSRF token, and the current revision. The generated
  template was regenerated.
- Lifecycle failures now redirect back to the editor with an escaped actionable
  `layout_error` message instead of rendering a raw HTTP error page. Successful
  cabinet archive/delete still redirects to the cabinet list; successful drawer
  lifecycle and detach actions redirect to the editor.
- Added a render regression that checks generated editor markup uses JSON field
  names and contains stable numeric URLs/CSRF controls, while rejecting the
  erroneous Go-style JS access. Full Go test/vet/build, templ/sqlc generation,
  production CSS build and JS suite passed. Browser re-test remains required.

## Git baseline

| Item | Baseline |
| --- | --- |
| Worktree | `/home/spetchal/Code/wledger-omo` |
| Branch | `transition/omo` |
| Starting HEAD | `05c0d5b021c8bed7a87b6f787dc0f337a7b50110` |
| Local `origin/main` reference at assessment | `05c0d5b021c8bed7a87b6f787dc0f337a7b50110` |
| Origin fetch/push URL | `https://github.com/magicaussie/wledger` |
| Local `origin/experiment/ai-handoff` reference | `4c390e8dbf7858b2801e9936e367447ba1705921` |
| Working tree before transition | Clean |
| Current reviewed local HEAD / M1 starting point | `0fc00cfb45cf29634515ab9e2fdad741cd55437d` |

These are locally inspected Git references. No fetch was performed and current
GitHub synchronization was not independently verified. The documentation
transition was independently reviewed and committed locally with explicit
authorization; it was not pushed.

## Responsibilities and delegation

GPT-6.1 Sol retains lead responsibility for important architectural, preservation
and security decisions. DeepSeek V4.1 Flash handles routine implementation and
tests; Sol is reserved for the important lead tasks, not routine implementation.

DeepSeek V4.1 Flash is the automatic delegated implementation worker for bounded,
routine coding tasks. Sol delegates suitable approved work through OmO without
asking the user to approve each routine delegation.

Every delegation must specify scope, permitted files, acceptance criteria,
tests and prohibited operations. DeepSeek returns changed files, implementation
evidence, test results, failures, unrun checks and uncertainties. Sol independently
reviews actual source changes and verifies tests before acceptance.

The owner's streamlined workflow, effective 2026-10-11, requires one Sol review
of delegated code followed by automated checks and ordinary in-scope corrections.
Do not create separate audit tasks or repeated review/approval cycles. Escalate
only architectural decisions, potential data loss, security issues or changes
outside the authorized scope. Keep reports concise: changes, test results,
blockers and next milestone. After M1 passes, present the proposed commit and
wait for authorization before committing or starting M2.

The documentation-only transition needed no implementation worker. M1 is
delegated through OmO to DeepSeek, with the two new Go files as its complete
edit scope; Sol owns this handover and independent review. Delegation never
grants commit, push, deployment, production database or WLED authorization.

## Architecture baseline and invariants

The existing application uses Go, SQLite/FTS5, Goose migrations, sqlc, templ,
HTMX, Alpine.js and WLED. The current storage model is controllers, containers,
bins, parts and part assignments, with walls as display groupings.

Confirmed constraints from the user and the root roadmap, including v0.2-v0.5:

- Legacy containers represent electrical strings/segments, not physical drawers.
- The owner has 69 physical drawers. Historical production evidence reports
  68 mapped database bins and two electrical containers. Their correspondence
  remains unresolved; do not invent a missing mapping or equate records by count.
- The product must support arbitrary cabinet sizes, drawers, bins, LED strings
  and wiring paths. Do not hardcode 69 drawers or a fixed cabinet topology.
- The current product concept assigns one WLED controller per cabinet, with
  multiple strings and multiple disjoint LED ranges per drawer.
- Physical drawer/bin geometry, LED placement and mounting, wiring visualization
  and electrical assignments are separate concepts.
- Mounting may belong to the fixed cabinet frame or moving drawer, including
  side, backlit, perimeter and custom placements.
- Wiring must support horizontal, vertical, serpentine, same-direction and
  arbitrary custom paths; display order is not electrical order.
- Stable identities are separate from editable labels, numbering, ordering,
  geometry and LED mappings. Layout changes must preserve inventory identities
  and quantities.
- `part_assignments` already supports multiple part types per bin and quantities
  per location. Preserve this source of truth.
- Removal of occupied storage requires an explicit relocation, archive or cancel
  workflow, not implicit cascading inventory changes.
- Opening a page or editor must not activate LEDs. Hardware tests require
  explicit authorization.

## Decisions and historical evidence

The takeover assessment and documentation transition are complete. Task 045
architecture planning was reviewed; its physical/electrical separation, stable
inventory identities and compatibility direction were accepted. The user has
authorized implementation of M1 only, not the cabinet redesign or migrations.

Preserve `experiment/ai-handoff` and its Task 044 / Sequence 77 record unchanged
as unapproved archival evidence:

- Task 044: read-only architecture and data audit for the cabinet/drawer/bin
  redesign.
- Sequence 76: ChatGPT request to DeepSeek, status `REQUESTED`.
- Sequence 77: DeepSeek response to ChatGPT, status `AWAITING_REVIEW`.
- Historical audited source: `8922670c148dfde836a1fcc93b770fe6e00c6b29`.
- The handover branch used a frozen application tree at
  `63016f4e75134ae0675934818fb77f53dd463800`; it is not the implementation baseline.

Sequence 77's proposal to reuse containers as physical drawers is superseded
by the later roadmap requirements. Its proposed migrations and implementation
slice are not approved. Do not resume the numbered handover protocol.

The historical claim that the drawer page automatically illuminates on load
is stale: the assessed source uses explicit manual Locate and has a regression
assertion in `internal/handler/drawers_test.go`.

## Risks and unresolved design decisions

- Reconcile the 68 legacy mapped bins with the 69 physical drawers using approved
  evidence before designing a production backfill.
- Define physical string/output versus WLED segment relationships and index
  semantics explicitly; do not assume they are interchangeable.
- Current containers and bins each support one contiguous allocation, not the
  required independent physical drawers with multiple LED ranges.
- `internal/hardware/service.go` matches bins by grid coordinates during saves.
  The new editors need stable-ID updates so moving geometry cannot replace
  inventory location identity.
- Decide LED overlap policy, multi-location Locate selection, timeout/stop
  behavior, geometry conventions and compatibility/rollback contracts.
- Application startup runs schema migrations and hardware data initialization.
  Starting the server is not a read-only inspection action.
- Historical production host information identifies `192.168.1.108`; the
  workstation Docker instance is unrelated. Neither was accessed. Historical
  production permissions do not carry into this session.
- `docs/roadmap.md` contains stale backlog claims. Use current source and the
  newer root `ROADMAP.md` when reconciling requirements.

## Historical transition verification evidence

The takeover assessment read the complete root roadmap, root development policy,
requested build/configuration/CI files, relevant schema and query sources,
application paths and representative tests. No nested `AGENTS.md` was found.

There are 92 Go test files in the assessed tree. Tests were inspected, not run;
several execute migrations and create files. No build, generators, application,
database migration or hardware operation was run for this documentation task.

Transition verification results:

- `git status --short`: exit 0; only ` M AGENTS.md` and
  `?? docs/OMO_HANDOVER.md`.
- `git diff --check`: exit 0; no whitespace errors.
- `git diff -- AGENTS.md`: exit 0; reviewed an append-only diff preserving
  every existing instruction.
- `git diff --no-index /dev/null docs/OMO_HANDOVER.md`: expected exit 1;
  reviewed the entire new document.

No check failed. The expected no-index exit code 1 indicates a new-file diff,
not a verification failure. The same checks are repeated after recording this
evidence, and final results accompany the review handoff.

CI verification for later approved coding work includes generated Go and CSS
consistency, `go build -tags fts5 ./...`, `go vet -tags fts5 ./...` and
`go test -tags fts5 -count=1 ./...`. These were not run in this transition.

## Task 045 implementation boundary

**Task 045 - Flexible Cabinet and LED Database Architecture**

Status: **architecture planning complete; M1 only authorized**.

Proposed scope: define the flexible cabinet/drawer/bin and LED architecture,
stable identity relationships, physical mounting and electrical addressing
separation, and evidence-based migration/compatibility strategy.

The finalized plan was delivered in the conversation. Exclusive controller
ownership, physical LED overlap prevention, multiple drawer ranges, independent
mounting/routes, preservation of stock and explicit legacy reconciliation are
confirmed requirements. M1 establishes preservation evidence without adding
storage entities, changing existing code paths or executing hardware actions.

## M1 implementation and review evidence

- Authorized files: `internal/hardware/compatibility.go`,
  `internal/hardware/compatibility_test.go`, `docs/OMO_HANDOVER.md`.
- Worker task: `st_01a126d4`, OmO `deep-low`, configured route
  `openrouter/deepseek/deepseek-v4.1-flash`, medium reasoning. OmO task metadata
  confirms the resolved model/provider, and the completed worker reported
  `PI_MODEL=deepseek/deepseek-v4.1-flash`, `PI_PROVIDER=openrouter`.
- Correction task: `st_01a12845`, the same configured model/provider and category,
  implemented the three diagnostic fixes and four new regression tests. Sol
  reviewed the returned changes once and directly tightened IPv6 authority and
  port-range validation before running the final automated gate.
- Worker scope: the snapshot helper and regression tests only. Sol independently
  inspected both files, reviewed the worker's original 15-test result, corrected
  in-scope defects directly and ran the final checks below.
- Implemented behavior: one consistent transaction; complete raw controller,
  container, bin, part, assignment and flag values; deterministic versioned
  inventory/mapping/target fingerprints; unchanged legacy target semantics;
  structured unresolved/invalid/unsafe diagnostics; errors for incomplete
  reads; no writes or external operations.
- Changed Go files: new `internal/hardware/compatibility.go` and
  `internal/hardware/compatibility_test.go`. This handover is the
  only changed existing file. No existing runtime hook, SQL, generated file,
  configuration or pre-existing test was changed.
- Fixture policy: temporary isolated SQLite databases and the existing schema
  only; recording or database wrappers must preserve integration behavior.
  The concurrency test uses a WAL database, a real writer, exact hook events
  and bounded contexts; there are no sleeps or polling. The no-write test uses
  SQLite query-only mode on a single fixture connection as well as before/after
  row, flag and audit evidence.

### Independent review findings and corrections

Sol identified and corrected the following before accepting M1:

- Target fingerprints previously included raw widths and stored coordinate
  representations. They now hash effective per-bin command/refusal intent;
  equivalent segment/container-relative representations and legacy width
  clamps keep the target digest while changing the raw mapping digest.
- Empty query results are canonicalized identically for nil and empty slices,
  without changing any row values or SQL NULL validity.
- The coordinate guard now precedes missing-parent checks, matching bin/part
  Locate behavior. Broken relationships remain separately reported; a missing
  controller is not reported as a resolved target.
- Negative allocation counts, invalid starts, negative segment IDs and negative
  controller capacities are reported without repairing the captured values.
- Invalid UTF-8 is rejected before hashing rather than normalized by JSON.
  Non-finite numeric serialization errors also return a zero baseline.
- Regression coverage now includes every snapshot getter's failure path, real
  SQLite serialization failure, equivalent target representations, empty row
  sets, malformed text, guard ordering and invalid allocation metadata.
- Corrupt-fixture helpers restore foreign-key enforcement before returning
  connections to the pool.
- Overlapping bin spans and positive valid allocations now produce deterministic
  paired-ID diagnostics, grouped by controller and segment. All overlapping
  pairs, including nested ranges, are reported; adjacent ranges and independent
  controllers/segments are not. Stored allocations and target intent are unchanged.
- Missing or syntactically unusable controller endpoints now produce warnings
  without network access or address normalization. Valid hostnames, IPv4,
  bracketed IPv6 and IPv6 zones are preserved; malformed literals and out-of-range
  ports are reported. The legacy stored Port remains excluded from wire targeting.
- Assignments referencing missing parts or non-NULL missing bins now produce
  per-assignment diagnostics. Legitimate NULL locations and zero quantities
  remain intact. Corrupt rows are preserved rather than hidden by joins or repaired.
- Four new regression tests cover overlaps, endpoint forms and dangling
  assignments, including query-only capture evidence. Diagnostics do not enter
  the fingerprints; the capture, legacy resolution and fingerprint functions
  remain unchanged.

No blocking review findings remain. Source inspection confirms the helper only
calls read queries inside one `ExecTx`; it has no audit, file, network or WLED
calls, and returned snapshots/fingerprint functions do not mutate stored rows.

### Final verification results

Toolchain: existing `/home/spetchal/.local/go-sdk/go/bin`, Go 1.25.5 linux/amd64.
The SDK directory was added only to command/process PATH; no installation or
configuration file change was required. `CGO_ENABLED=1` was used for Go checks.

| Check | Actual result |
| --- | --- |
| `gofmt -d internal/hardware/compatibility.go internal/hardware/compatibility_test.go` | Exit 0, empty diff |
| `go test -race -tags fts5 -count=1 -run '^TestCompatibility' -v ./internal/hardware` | Exit 0; all 24 top-level regression tests passed, no race reports |
| `go test -tags fts5 -count=1 ./...` | Exit 0; 40 test-bearing packages passed |
| `go vet -tags fts5 ./...` | Exit 0 |
| `go build -tags fts5 ./...` | Exit 0 |
| `git diff --check` | Exit 0, no whitespace errors |
| Git scope checks | Only the three authorized files changed; index empty |

New-file diff inspections used `git diff --no-index /dev/null <file>`; exit 1
means the new file differs from `/dev/null`, not a failed check. HEAD remains
`0fc00cfb45cf29634515ab9e2fdad741cd55437d` at the recorded verification checkpoint.
The owner subsequently accepted M1 and authorized the local commit. Its hash is
reported after creation; it cannot be embedded in its own committed contents.
No push or merge is authorized.

### Limitations

- This is a complete snapshot of the approved core tables and three relevant
  flags, not a full database backup or migration authorization.
- LED fingerprints bind legacy API-level command intent, not actual firmware,
  segment transforms, physical wiring or device availability. No hardware was
  contacted. Legacy drawer/global-off behavior is not a new Locate workflow.
- Unknown/unresolved coordinates and unsafe mappings are captured as findings,
  not silently repaired; a complete snapshot with warnings is not proof that a
  migration or hardware action is safe.
- The mapping fingerprint includes complete row metadata and controller
  observations. Those can change independently of the target fingerprint.
- `ExecTx` provides a consistent snapshot, not a latest-state guarantee.
- The no-write guarantee covers application data, schema and audit mutations,
  not an absence of SQLite's own WAL/shared-memory filesystem housekeeping.
- `gopls` is not installed; no installation was authorized or performed.
  Compiler, targeted/full tests and vet provided the Go verification.

Approved commit: `feat: add read-only compatibility baseline`.

## Proposed M2 scope - additive storage foundation

Planning only; no M2 implementation or migration execution is authorized.

- Add cabinet records with exclusive controller ownership, stable drawer records
  with editable labels/order/geometry, and a separate drawer-to-existing-bin
  association table. Keep existing bin IDs and `part_assignments` unchanged.
- Add multiple drawer LED-range child records with explicit addressing semantics;
  keep electrical assignments separate from layout geometry, mounting and wiring
  paths. Do not equate physical strings with WLED segments or repurpose legacy
  containers as drawers. Final address semantics are an architectural prerequisite
  to approving M2 DDL; mounting/path editors are outside this milestone.
- Use additive Goose migrations and sqlc models/queries only. New tables start
  empty: no inferred cabinet/drawer creation, automatic legacy conversion, count
  reconciliation, or default 69-drawer layout. Existing runtime paths stay active;
  API/UI/Locate cutover is outside M2.
- Preserve all existing controller/container/bin/part/assignment rows, IDs, flags,
  quantities and mappings. Use foreign keys and restrictive deletion for new
  relationships; no inventory cascades. Rollback must leave legacy data unchanged
  and refuse to discard populated new tables without a separate explicit decision.
- Required tests: empty and legacy-populated fixture migrations; all three M1
  fingerprints unchanged before/after migration; upgrade/rollback preservation;
  ownership, foreign-key and safe-deletion constraints; multiple drawers/ranges,
  geometry/electrical independence and range validation; generated-code
  consistency, full tests, vet and build. Fixtures only, no production or hardware.

## Coordinated development checkpoint

The current assignment authorizes M2-M4 and local milestone commits. DeepSeek
implemented independent physical-schema, migration-test and backup-envelope
tracks in isolated worktrees; integration used patches, not Git merges.

- Shared physical foundation is integrated: migration 011 adds empty cabinets,
  drawers and existing-bin attachment tables, restrictive foreign keys and an
  empty-only rollback guard. Existing model structs and legacy records are unchanged.
- Backup envelope guards reject unsupported/missing versions, duplicate/mixed
  manifests, duplicate/unknown top-level JSON fields and invalid coordinate
  markers before cleanup or mutation. Valid legacy version 1.0 remains supported.
- Seven physical migration tests passed with exact M1 fingerprint/raw-row/flag
  preservation, populated-rollback refusal and occupied-storage deletion guards.
- Integrated verification passed: race-enabled preservation/compatibility tests,
  the full Go suite (40 test-bearing packages), vet, build, whitespace checks and
  sqlc generation consistency.
- No new Locate behavior, electrical DDL, production access, deployment, hardware
  operation, milestone commit, push or merge has occurred.

The owner resolved milestone framing in favor of the finalized Task 045 plan:
M2 backup guards, M3 physical schema/backup, M4 reconciliation/legacy guards.
The additive-schema-first proposal above is historical and does not rename the
approved milestones. M2's commit includes only backup guard source/tests and this
handover; the additive physical foundation is being integrated for M3.

Addressing is resolved for future DDL: legacy containers store WLED segment IDs,
not physical outputs/strings. WLED individual LED indices are segment-based and
grouping, spacing, reversal and mirroring affect physical targeting. Distinct
physical pixels `(string_id, pixel_index)` and explicit segment-address mappings
are required; unverified or unsupported profiles must not translate or activate
new Locate. No legacy offset or physical correspondence is inferred.
Reference: https://kno.wled.ge/interfaces/json-api/#per-segment-individual-led-control

M2 committed locally: `37fd555deb6873d09d9955072f916d81f9514c01`
(`feat: guard backup envelopes before restore mutation`). Before committing,
the exact staged M2-only tree was exported into an isolated temporary checkout;
backup guard race tests, the full Go suite, vet and build all passed there.
The uncommitted physical foundation was excluded from that commit.

M3 implementation is integrated: additive cabinets, drawers and bin attachments
with restrictive foreign keys and no legacy backfill; complete version 2.0
`storage_data.json` backups capture legacy data, physical rows and all system flags
in one transaction. Restore preserves IDs, quantities, mappings, revisions and
timestamps. Incomplete, ambiguous or contradictory envelopes are refused before
cleanup or mutation. Legacy 1.0 restore refuses a populated physical target.
Migration rollback refuses populated new tables.

Current snapshots preserve legacy mappings exactly; legacy allocation heuristics
apply only to legacy archives because their segment-only grouping can conflate
separate controllers. No hardware addressing or Locate behavior changed.

M4 reviewed reconciliation and legacy grid/import guards are implemented and their
targeted race tests passed. Milestone verification and local commits remain
ordered M3, M4. M5 and electrical-topology implementation remain unauthorized.

M3 committed locally: `deef50b4d508932e01aff1872e22b549e2cdd720`,
parent `37fd555deb6873d09d9955072f916d81f9514c01`. Its exact staged tree
excluded M4 and passed database/backup race tests, the full Go suite, vet, build,
gofmt and sqlc regeneration consistency. Tests prove additive migration preserves
all three M1 fingerprints, raw legacy rows and flags; complete backup round-trips
preserve those values and the physical graph. Rejected envelopes and transaction
failures leave database and filesystem sentinels intact.

M4 reconciliation is an explicit service workflow, not an automatic migration:
preview is read-only and reviewed apply validates the entire batch in an immediate
transaction before inserting attachments. Invalid geometry, out-of-bounds
placement, missing or archived storage, controller mismatches, duplicate bins,
stale reviews and conflicting bindings are blocked. Exact reviewed replay is a
no-op; successful attachment batches increment each affected layout revision once.
Failures roll back attachments and revisions and return no partial success.

Legacy grid saves refuse reconciled controllers before mutation. Config import
checks every existing controller with the same IP, including duplicate legacy IPs,
and refuses any reconciled match. Unreconciled operations retain existing behavior.
These guards preserve inventory identities, quantities, legacy mappings and flags.
No new handler, editor, Locate path or electrical topology is introduced.

M4 verification passed: reconciliation and legacy guard race tests, the full Go
suite (including migration and backup regressions), vet, build, gofmt, whitespace
and sqlc regeneration consistency. The milestone contains this handover,
`internal/storage/reconciliation.go`, its tests, hardware service/config guards
and `internal/hardware/reconciliation_guard_test.go`; its parent is M3
`deef50b4d508932e01aff1872e22b549e2cdd720`.

No check failed. gopls was unavailable; compiler, vet and test checks were used.
No application startup, production database, deployment or hardware operation was
performed. Commits remain local on `transition/omo`; nothing was pushed or merged,
and remote synchronization was not verified. Version 2.0 archives require this
reader: old binaries cannot restore the physical graph. Populated physical schema
rollback and legacy restore into populated physical storage intentionally refuse.

The authorized M2-M4 batch ends at its M4 local commit. Recommended next milestone
is M5 stable bin identity decoupling, only after separate authorization; electrical
topology remains M8. Production reconciliation of 68 bins and 69 drawers still
requires reviewed evidence and is not inferred by these services.

## Task 045 M5 — Bin Decoupling

**Status:** Completed and committed locally. The owner explicitly authorized
implementation, automated tests and one local commit on 2026-10-11.

**Commit:** `2e6056f9f9c176270a231c42df97f4e9082053f9`
(`feat: decouple bins from legacy containers`), parent
`b9d52ab1b3ee2fd7dd403c74d4f32f0e7135e1bf`. No push, merge, deployment,
production database access or WLED operation occurred.

**Baseline:** branch `transition/omo`, commit
`b9d52ab1b3ee2fd7dd403c74d4f32f0e7135e1bf`, parent
`deef50b4d508932e01aff1872e22b549e2cdd720`. The worktree was clean before M5;
the branch was four commits ahead of `origin/transition/omo`.

**Scope and decisions:** Migration 012 rebuilds `bins` to make
`container_id` nullable while preserving bin IDs, AUTOINCREMENT high-water,
assignments, quantities, legacy electrical columns, foreign keys, delete actions,
unique constraints and indexes. NULL means no legacy electrical-container
association; it does not imply or synthesize a drawer assignment. Physical
attachment remains an explicit reviewed operation and is independent of the
optional electrical association. Controller-mismatch validation applies only
when the bin has a legacy container. The legacy mapped-bin Locate path remains
unchanged; bins without an electrical container are refused before WLED access.
Rollback restores NOT NULL only when every bin has a container; otherwise it
refuses without inferring or backfilling a value.

**Delegated implementation:** `st_01a128ac` (DeepSeek V4.1 Flash via OpenRouter,
deep-low, medium) implemented migration 012 and migration preservation/rollback
tests. `st_01a128ad` (DeepSeek V4.1 Flash via OpenRouter, deep-low, medium)
updated sqlc-generated models and nullable call sites. `st_01a128ae` (DeepSeek
V4.1 Flash via OpenRouter, quick, low) added query regressions. Luna reviewed the
integrated null-ID behavior and corrected reconciliation so an explicit
physical attachment does not require an electrical container.

**Final verification:** Migration forward/rollback tests passed, including the
rollback refusal and same-named main-table preservation checks. The final
`go test -tags fts5 -count=1 ./...` passed; `go vet -tags fts5 ./...`,
`go build -tags fts5 ./...` and the production server binary build all passed.
Two `sqlc generate` runs produced no generated-file changes. `gofmt -d` and
tracked/untracked whitespace checks were clean. `gopls` is not installed; the
compiler, vet and tests provided source verification. The independent acceptance
audit and authorized local commit remain pending.

**Open risk:** Older application binaries expect non-NULL `bins.container_id`
when scanning bin rows. Existing legacy rows and their behavior are preserved,
but binaries predating M5 are not compatible with rows deliberately created
without a legacy container. Rollback intentionally refuses while any bin has
NULL `container_id`.

## Task 045 M6 + M7 — Physical Browsing and Stable Layout Editing

**Status:** M6 implementation is authorized and in progress. M7 is also
authorized to begin automatically after M6 passes its gates and is committed as
a separate local commit. M8 and later remain out of scope.

**Authorization:** Owner message on 2026-10-11 authorizes M6 and M7 source work,
automated tests, integration and one local commit per verified milestone. No
push, merge, deployment, production access or hardware operation is authorized.

**Baseline:** branch `transition/omo`, commit
`2e6056f9f9c176270a231c42df97f4e9082053f9`, parent
`b9d52ab1b3ee2fd7dd403c74d4f32f0e7135e1bf`; worktree was clean before this
task. The target worktree is `/home/spetchal/Code/wledger-omo`; the separate
original checkout `/home/spetchal/Code/wledger/wledger` on
`experiment/ai-handoff` remains untouched. The target branch was five commits
ahead of `origin/transition/omo`.

**Architecture constraints:** Continue using `cabinets`, `drawers`, and
`bin_drawer_attachments` as physical storage truth. Legacy `containers` remain
electrical strings/segments and are not renamed or treated as physical drawers.
Browse and select bins by stable `bins.id`, including bins whose
`container_id` is NULL. NULL electrical ownership does not imply a physical
drawer; only an explicit user selection may attach a bin. Physical browsing or
editing must not change part assignments, quantities, electrical mappings or
the existing Locate path.

**Milestone gates:** M6 adds physical cabinet/drawer/bin browsing and bin
selection; M7 adds reviewed physical layout editing with stable identities,
validated geometry, stale/conflicting edit protection, transaction integrity
and rollback. Each milestone receives its own targeted and full Go verification,
generated-code/formatting checks, independent integration review and local
commit. M7 starts only after M6 verification and commit.

**Delegation in progress:** Three independent read-only DeepSeek V4.1 Flash
investigations were launched for routes/handlers, storage/backup queries, and
web templates; a fourth source investigation maps stock assignment/bin-filter
behavior. M6 read-model and UI-design-document implementation tasks are also
delegated with disjoint file ownership. The peak concurrent worker count so far
is three. Detailed model token usage and verified implementation evidence will
be added as the milestones progress.

### M6 implementation decisions and QA corrections

M6 remains in progress and has not been committed. The M6 browse service reads
physical placement only from `bin_drawer_attachments`; it displays stock through
stable bin and part IDs, keeps exact quantities (including zero), and never
reads `bins.container_id` to determine physical location. Browse routes are
read-only; the bin picker stores the selected existing `bin_id` without
creating or changing a physical attachment or electrical mapping. Existing
backup schema and electrical/Locate behavior are unchanged.

Two independent read-only QA reviews identified additional M6 corrections:

- A bin attached beneath an archived cabinet/drawer was omitted from the
  active hierarchy and incorrectly excluded from the unplaced group. The
  picker now has a separate inactive/unavailable physical-placement group;
  truly unattached bins remain in the distinct “No physical placement” group.
- Picker data was composed across multiple transactions. `BrowseService`
  now creates the complete active/inactive/unplaced partition in one deferred
  read transaction. A deterministic WAL test commits a concurrent attachment
  between the attachment read and subsequent reads and verifies the picker
  returns one consistent pre-write snapshot.
- Inventory rows with identical part names now use stable part IDs as their
  deterministic ordering tiebreak.
- Visual QA found duplicate cabinet/bin IDs, undersized picker touch targets,
  wrapping count badges, a non-warning count using warning color, and missing
  selected/focus styles in the stale CSS. Templates now show each identity
  once, keep count badges intact with neutral styling, render a selected wash
  plus check glyph, enforce a 44px button target, and provide an immediate
  2px primary focus outline with no transition. Long bin names truncate within
  the button. Unnecessary button scaling motion was removed.
- The first rendered part-detail phone capture exposed an overflowing
  PageHeader action row. The shared header now wraps actions within its
  available width; a fresh 390px DOM probe reports the actions fit and the
  document width remains 390px.
- The original phone captures showed the navigation drawer during its closing
  transition after device emulation, rather than a product navigation defect.
  Follow-up captures must apply device emulation after navigation and await the
  drawer's `visibility` transition before taking the screenshot.

Both independent visual QA passes A and B completed with REVISE. Their
141-image matrix at `/tmp/wledger-m6-captures-final-1791705950361-08b157/`
was validated, but it predates the latest review-driven locale, contrast and
picker changes and is no longer acceptance evidence for the current tree.
Pass A found selected-glyph row reflow and theme-transition capture artifacts.
Pass B found muted-text contrast problems, raw low-stock keys in the old CJK
captures, and the same theme capture artifact. The current working tree now
contains changes for those product findings, but no fresh screenshots or final
review pass have been completed.

After the picker, Chinese punctuation, regional Chinese-tag handling, muted
text contrast across the M6 inventory surfaces, locale and test diagnostic
corrections, the current shared-tree gates passed:
`PATH=/home/spetchal/.local/go-sdk/go/bin:$PATH CGO_ENABLED=1 go test -tags
fts5 -count=1 ./...`, `go vet -tags fts5 ./...`, and `go build -tags fts5
./...`. `npm run build:css` passed and regenerated
`web/static/css/output.css` (SHA-256
`c693764b763c237cdc44f7e503da754bf9bd47c4ea986c78a6ce0061eeb5062f`).
`git diff --check` passed. A read-only isolated `templ v0.3.977` audit
regenerated all 91 Templ outputs twice and confirmed byte-identical results
before the final contrast changes; after those changes `templ generate` ran
successfully at the repository root to update generated counterparts.

The two 141-image visual reviews remain REVISE evidence only for the preceding
source snapshot. The current source contains fixes for their product findings,
but requires fresh browser captures after theme transitions settle, with
selected-state frames taken after removing keyboard focus. Final independent
visual approval is still pending. BrowserSkill setup identified Google Chrome
as the workstation's default and in-use browser, installed its CLI/daemon, but
could not register the extension because the Chrome Web Store extension path
requires administrator rights. The exact remaining setup instruction is
available to the owner in the current session.

The owner resumed M6 work on 2026-10-11. M6 remains uncommitted until fresh
visual acceptance; M7 has not started and remains gated on the separate M6
commit. `docs/AI_DEVELOPMENT_HANDOVER.md` is a stop-time snapshot, not the
current source of truth. The original `/home/spetchal/Code/wledger/wledger`
checkout remains untouched. No push, merge, deployment, production access or
hardware operation occurred.

---

### Task 054B — implementation published; independent review pending

Implementation commit: `71a2cd73c468a29fd34d018dc39cc4d5e821ecc2`, parent
`68a3821459e359ec2032fbbfd4e810008f71e496`. No schema migration was added.

SQLite `BEGIN IMMEDIATE` is the shared WLEDger operation claim. Locate resolves
mapping and reserves its session/target snapshots in one claim. Activation
attempt markers are durable before POST; every physical write reloads session,
target, endpoint, ranges and commands under the claim. Session state and target
ownership updates use compare-and-set predicates. Target ownership clears only
with acknowledged, exact range cleanup in the same claim. Legacy WLED writes,
backup restore and relevant capacity/controller/wiring/allocation mutations
share the claim and fail closed on unresolved ownership.

Verification passed: `go test -tags fts5 ./...`,
`go test -race -tags fts5 ./...`, `go vet -tags fts5 ./...`,
`go build -tags fts5 ./...`, and `git diff --check`.

Tests include dual-connection Locate/Stop, concurrent Locate and recovery,
configuration/legacy command contention, conservative target ownership after
uncertain results, restart cleanup, and subprocess termination releasing the
SQLite transaction claim. All controller tests use local mocks; no production
database, physical controller or deployment system was accessed.

Limitations: only cooperating WLEDger instances sharing a supported local
SQLite file are coordinated. External WLED applications are not fenced, and
SQLite cannot retract an HTTP request already received by a controller.
Independent GitHub source review is the next acceptance gate. Existing unrelated
M6/handover working-tree changes remain uncommitted and excluded from this task.

---

## Task 068 — Physical WLED Output Acceptance — 2026-10-15

- **Baseline/branch:** requested candidate `c6b16aa448392353252976dde7b78e5f2586a5e3` on `development/chatgpt-m6-m7`; checkout matched this baseline and `origin` at task start.
- **Authorization/safety:** owner requested source review, software fixes, verification and publication. Do not access production, enable physical output, or contact real controllers; no such operations were performed.
- **Finding/fix:** physical Locate already checked WLED JSON acknowledgements, but legacy state writes and Global Off accepted any HTTP 2xx, including firmware `success:false`. All WLED state writes now require a valid `{"success":true}` acknowledgement and include firmware error details. Mapping HTTP failures report endpoint, status and response body.
- **Acceptance evidence:** added representative WLED 0.15-shaped `/json/info`, `/json/cfg`, and `/json/state` fixtures. The integration test asserts mapping verification's read-only GETs, Locate and Stop request sequence, exact `/json/state` range payloads and content types, and that unrelated mock pixels remain unchanged. Existing suites exercise inventory-product/bin routing, up to five controllers, output/segment mapping, stale layouts, multi-process coordination, timeout, restart recovery, targeted cleanup and Global Off.
- **Operator documentation:** `docs/WLED_PHYSICAL_COMMISSIONING.md` and `docs/environment.md`; Compose passes `WLEDGER_PHYSICAL_OUTPUT_ENABLED` with a default of `false`.
- **Verification:** `/home/spetchal/gotoolchain/go/bin/go test -tags fts5 -count=1 ./...`, `go test -race -tags fts5 -count=1 ./...`, `go vet -tags fts5 ./...`, `go build -tags fts5 ./...`, `docker compose config --quiet`, and `git diff --check` passed. Tests use local mocks only.
- **Limitations:** actual firmware, GPIO wiring, physical strip order, power/network behavior and external writers cannot be validated by software fixtures. Commissioning requires separate owner authorization. Existing cabinet assignment is limited to five controllers, and supported physical mapping remains limited to the documented linear RGB topology. No separate model was delegated.
- **Publication:** commit `0b48621a4b43c2c60ed544c4c4d259bfe836322d`, parent `c6b16aa448392353252976dde7b78e5f2586a5e3`, pushed to `origin/development/chatgpt-m6-m7`; post-push fetch confirmed local/remote equality. Independent published-source review remains pending. Existing unrelated working-tree changes remain excluded.


## Isolated RC orphan cleanup race fix — 2026-10-16

- **Authorization/baseline:** owner approved an isolated correction and local
  commit, explicitly prohibiting push, merge and deployment. Chosen worktree:
  `.rc-cleanup-worktree`, branch `fix/rc-orphan-cleanup-race`, baseline `b81052d`.
  Production data, running instances, ports 8090–8093 and physical hardware
  remain untouched. Docker verification uses only disposable test containers.
  Implementation and validation were delegated using the configured default
  model; the lead reviewed the actual source and writer/lock ordering.
- **Root cause:** the orphan service queried references, then removed files
  without excluding reference creation. The maintenance gate admits ordinary
  requests concurrently (shared locks); acquiring its exclusive lock from the
  cleanup handler would upgrade an already-held shared lock and deadlock.
- **Protocol:** `maintenance.UploadReferences`/`UploadCleanup` use a separate
  cross-process flock inode at `maintenance.LockPath(uploadsDir) + ".uploads"`.
  This retains mount-aware, restore-stable placement. Reference writers take SH
  before file publication or SQLite work and hold it through commit/rollback and
  cleanup. Delete takes EX before its authoritative rescan and holds EX through
  every removal. Public Scan takes SH to exclude restore but remains advisory
  relative to other SH writers. Lock acquisition polls nonblocking flock with
  request cancellation; the lock file itself is opened with O_NOFOLLOW.
- **Lock order:** maintenance gate (when applicable) -> upload claim -> SQLite.
  No upload operation acquires/upgrades the maintenance gate. Restore rejects a
  shared upload context before acquiring its maintenance gate; normal restore
  and startup recovery acquire upload EX after maintenance EX and before their
  database/filesystem work. Transactional document calls reuse a context-bound
  pre-transaction SH claim; an unclaimed transaction querier is rejected before
  any file I/O. Standalone Store document calls acquire their own SH claim.
- **Reference-writer trace:** parts CreatePart/UpdatePart cover image processing,
  document uploads inside ExecTx, commit/rollback, and failed-upload cleanup.
  Category create/update handlers cover image processing through Save and file
  cleanup; Save also participates for direct callers. Supplier ImportFromProvider
  and ResyncFromProvider cover downloaded images through reference persistence.
  Backup Restore and startup recovery cover generation swaps and restored part,
  category, and document references. CSV bulk import sets no upload references.
  Generated db query methods remain low-level APIs: callers adding reference
  mutations must participate before starting a transaction.
- **Path/late-writer safety:** Scan ignores symlinks, nonregular entries and hidden
  control/recovery paths. Delete rechecks regular-file type and removes using
  os.Root, confining resolution even if a parent is replaced with an escaping
  symlink. Category Save checks the file under SH using the same confined root,
  rejecting missing/nonregular images rather than committing a reference after
  cleanup already removed it. A custom-uploads category constructor keeps tests
  and other custom namespace callers on the correct claim/file namespace.
- **Audit/partial errors:** Delete retains exact successful paths on later error
  or cancellation; the HTTP handler audits those paths before returning failure.
  Audit uses a bounded 5-second context detached from request cancellation and
  surfaces audit insertion failure rather than returning a success redirect.
  File removal and SQLite audit insertion are NOT crash-atomic; process death
  after unlink or a failed audit write can leave an unaudited deletion. No new
  crash journal is claimed or introduced.
- **Tests:** a real subprocess, independent flock and SQLite connection pauses
  reference creation strictly after initial identification. A cleanup attempt
  must time out without removing anything while that writer owns SH. The writer
  then commits through the category service; a stale-path Delete revalidates and
  preserves the image/thumbnail, with the reference and PRAGMA integrity_check
  verified. Additional tests cover post-cleanup reference rejection, symlink and
  traversal exclusion, upgrade rejection, unclaimed document transactions, and
  audit insertion failure after successful removal. Existing backup category
  round-trip fixture now explicitly supplies its custom uploads namespace.
- **Verification passed:** targeted normal tests for uploads, maintenance, parts,
  categories, documents, suppliers, handler, backup; final
  `go test -race -tags fts5 -count=1` and `go vet -tags fts5` for the same eight
  packages; 20 repetitions of the three new cleanup race/path/late-reference
  tests; `git diff --check`. Go tools used from
  `/home/spetchal/gotoolchain/go/bin`. Full build, vet, Go and race suites,
  npm tests, layout browser (five runs), physical-first, barcode, CSRF, UI-only,
  fresh-install Locate and inventory/restore suites passed before commit.
  Physical-first initially required linking existing node_modules in this
  isolated worktree; it passed after that environment-only correction.
  Full gates are repeated against the final local commit before reporting.
  Initial verification caught
  and corrected a duplicate import, a fixture using the wrong uploads namespace,
  and a missing test error responder.
- **Remaining limits:** advisory flock protects cooperating upgraded processes
  only, on supported local filesystems. Old binaries, manual SQL/filesystem edits,
  lock-file replacement, malicious control-directory changes, or differently
  configured/aliased upload namespaces are outside the contract. All processes
  sharing a namespace must be upgraded together and resolve the same stable lock
  location. Long supplier network calls/uploads can delay cleanup or restore;
  flock does not guarantee fairness. An existing category whose image is already
  missing will now need its image cleared/replaced before Save succeeds. No
  schema migration or operational action is required by the source change.
- **Status:** correction prepared for an authorized local commit and full
  post-commit verification. Publication requires new explicit owner approval;
  independent source review remains required before merge or deployment.

## Active owner request — LED operation ownership and Test Centre — 2026-10-16

- **Current authorization:** the owner's current request supersedes the historical Task 054A design-only restriction. Implement concurrent non-overlapping physical-pixel operations, explicit Locate conflict protection, selected-operation Stop, separately confirmed Emergency Global Off, configurable function colors/brightness, Test Centre, guided mapping and simulator tests. Keep multiple-range drawer allocations and inventory identities. No merge, deploy, production changes, coordinate conversion or physical controller contact is authorized.
- **Baseline:** `development/chatgpt-m6-m7`, `6dc85d31337d6eb3b8b1d28370330a5880cea9a2`. Fetch confirmed equality with `origin` before publication preparation. The original dirty `AGENTS.md`, historical handover changes, archival worktree/staging directories and untracked audit documents are preserved and excluded from this task's publication.
- **Architecture/implementation:** sessions are keyed by operation ID; physical ownership is keyed by live MAC/electrical output pins/pixel intervals. Immediate SQLite claims plus CAS fence activation/Stop/recovery/emergency across processes. Read-only preflight rejects already-known overlaps while another process holds a command claim; authoritative live identity validation remains inside the writer transaction. Stop identifies the exact operation. Legacy writes remain conservative. Recovery never restarts illumination patterns.
- **Emergency safety review:** initial source review identified request-cancellation coupling, loss of per-controller acknowledgement evidence, omitted historical endpoints and ambiguous drawer Stop. These were corrected: accepted emergency delivery uses service-owned bounded contexts, independent durable endpoint outcomes, generation fencing and a retained global barrier. Historical unconfigured endpoints are reported unattempted rather than contacted blindly. Drawer Stop now submits/validates the displayed operation ID. Failed tests remain visible after cleanup.
- **Mapping/settings:** additive bin overrides reference immutable allocation identities and zero-based offsets, retaining topology/address snapshots. Explicit clear/reassign is available; legacy bin LED indexes remain unchanged. Guided endpoint and sequential wizards append atomically after confirmed validation. Function styles persist in system flags, with HEX/RGB, brightness, save/reset and screen-only preview. Existing legacy colors remain until the first central save/reset. Backup captures overrides/flags and refuses older-archive omission that would lose overrides.
- **Migrations:** 025 concurrent ownership, 026 physical bin pixels/guards, 027 emergency delivery evidence; lossless upgrade and guarded rollback tests. sqlc-generated contracts remain generated; LED helpers use the separate `LEDQuerier` extension. Regeneration was tested.
- **Delegation:** bounded ownership, function settings, interface, mapping/backup, simulator and test tasks used only the same preset GPT-6.1 Sol model. Worker reports were followed by lead source inspection, integrated builds/tests and browser execution. An interrupted regeneration-safety subtask was completed by the lead. No delegated worker published changes.
- **Verification:** full Go suite and race suite, vet/build, JavaScript tests, sqlc regeneration, new isolated LED browser flows, CSRF browser, layout editor, physical-first QR/bin lifecycle, barcode scanner and extended Task 067 fresh-install/restore/SIGKILL/multi-process acceptance passed during integration. The existing QR browser dependency (Python OpenCV) was installed only inside a disposable validation container. New LED browser tests use loopback mocks with external networking disabled; older acceptance scripts use loopback mocks and CDN UI dependencies. No real controller or production service/database was contacted.
- **Operator guide:** `docs/WLED_LED_SYSTEM.md`; simulator: `docs/WLED_SIMULATOR.md`. Entry point: Hardware → LED Test Centre. Known limitations include unknown pre-test framebuffer restoration (Stop clears owned ranges only), external WLED writers, supported RGB/solid topology, existing five-controller cabinet membership limit and unverified physical behavior.
- **Final lead verification/publication gate:** full Go tests, full race tests, vet/build, JavaScript tests, sqlc regeneration and the integrated isolated LED browser test passed again after the final source changes. Compact WLED MAC normalization and legacy-color preservation regressions are covered. CSS production generation passed with the existing `@property --radialprogress` optimizer warning. Publish only this scoped verified checkpoint on the authorized branch; independent published-GitHub source review and separately authorized physical commissioning remain outstanding. No merge or deployment is implied.

## Owner request — integrate the published LED implementation into the port-8094 installation — 2026-10-16

- **Goal/authorization:** integrate the published LED implementation into the existing
  clean-install release candidate and update the isolated port-8094 installation to one
  combined application, preserving its administrator, settings and data. Physical WLED
  output stays disabled; ports 8090–8093 and their data stay untouched; no merge to
  `main`, no physical controller contact, no deployment beyond the isolated 8094 instance.
- **Integration branch/commit:** `integration/led-into-rc-8094`, merge commit
  `55f444e51cf83ac2ddc27638c2e2c8facc70cf1c` (worktree `.led-8094-integration`).
  Parents: `909d59f` (release-candidate/clean-8094, first parent) and
  `b235bfd` (development/chatgpt-m6-m7 LED implementation). Merge base `6dc85d3`.
- **Conflicts resolved (2):** `docs/OMO_HANDOVER.md` (retain both sections) and the
  generated `web/pages/settings_templ.go` (regenerated with templ v0.3.977 from the
  cleanly merged `settings.templ`, so the LED function settings and the
  upload-maintenance link coexist). Migration numbering is compatible: the release
  candidate tops out at 024; the LED implementation adds 025–027.
- **Source verification on the merged tree:** `go build -tags fts5 ./...`,
  `go vet -tags fts5 ./...`, `go test -tags fts5 -count=1 ./...`,
  `go test -race -tags fts5 -count=1 ./...` (73 packages each), `sqlc generate`
  (deterministic, no diff) and the JS suites all passed. Browser suites against loopback
  simulated controllers passed: LED Test Centre (`realLocalAPI` + Emergency Global Off),
  CSRF, layout editor, physical-first (asserts `physicalOutputEnabled:false`, no WLED
  requests), barcode scanner, UI-only install, fresh install, inventory lifecycle +
  restore, restore-only and Locate/multi-process (incl. SIGKILL crash recovery).
- **Backup and dry-run:** a consistent `sqlite3` backup of the live 8094 database plus
  uploads/logs archives was taken to `.8094-backups/20261016-pre-led-integration/`
  (`integrity_check ok`, admin and settings intact) and booted successfully as a scratch
  instance. A dry run of the new image against a copy of that data migrated to 027 with
  the administrator and settings preserved before the live swap.
- **Deployment:** image `wledger:led-8094-55f444e` built from the merge; the existing
  `wledger-rc-8094` container was recreated in place with the same named volumes
  (`wledger-rc-8094-{data,uploads,logs}`) and port `8094:8080`,
  `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false`. It is healthy, migrated to 027, and the
  administrator `me@mustbemicks.com` and settings
  (`require_auth_for_read=1`, `locate_timeout_seconds=10`) are preserved.
- **Live simulated-controller acceptance:** a disposable container from the same image
  plus the project's `cmd/wled-simulator` in a shared network namespace passed first-run
  setup, controller save, Test Centre connection/pixel test, Stop, Emergency Global Off
  (acknowledged), function-colour save and restart persistence, with no page errors.
  Physical output was enabled only for that disposable simulator instance.
- **Publication status:** the merge is committed locally on `integration/led-into-rc-8094`
  and is a clean fast-forward for `development/chatgpt-m6-m7` (`b235bfd` is an ancestor).
  It was **not** pushed: publication direction (merging the release-candidate lineage into
  the authorized development branch) needs explicit owner approval before a commit/push to
  `origin/development/chatgpt-m6-m7`. No merge to `main`, release, deployment or physical
  WLED operation was performed.

## Graphical LED Wiring Designer — approved implementation, not deployed — 2026-10-17

- **Authorization:** owner approved implementation of the graphical designer and
  simulator-only automated/browser verification. Do not deploy, modify the running
  installation, contact physical controllers or enable physical output. This task
  does not authorize merging to main or publishing from an unapproved branch.
- **Baseline/worktree:** integrated application at `f546493`, branch
  `integration/led-into-rc-8094`, worktree `.led-8094-integration`. The existing
  untracked `.live-e2e/` and `compose.led-8094.yaml` are preserved. Changes remain
  uncommitted/unpushed; current publication branch is not the authorized development
  branch. The running 8094 image and its data were not changed for this task.
- **Implementation:** controller-level SVG designer with straight, rectangular,
  irregular row/column and custom-pixel layouts; independent output groups; split
  strip sections with independent offsets/rotation; output/pixel dragging; zoom,
  pan and focus controls; physical numbering, traversal arrows and endpoint labels;
  zero-pixel jumpers; bounded raster background placement; save/reopen. Cabinet
  path editing additionally has explicit Arrange/Edit Wiring modes and a variable
  row-length path generator.
- **Preservation architecture:** graphical metadata is an additive versioned system
  flag, not controller output configuration. Visual revisions and transactional
  topology signatures reject stale saves. Stable output IDs reference electrical
  configuration. Existing topology resolution, drawer allocations and bin overrides
  remain authoritative. No migration or automatic conversion is introduced.
- **Electrical changes:** separate administrator-only review/apply endpoints with
  exact `APPLY WIRING` confirmation, repeated validation, known/unique output IDs,
  topology signature and operation-claim serialization. Existing cabinet membership
  (including legacy cabinet references), mapped bins, active/unresolved ownership
  and emergency barriers conservatively prevent electrical changes. Eligible
  unmapped controllers can add/reorder/resize/reverse outputs without replacing
  retained IDs. Drawing edits remain available for protected installations.
- **Integration:** Hardware and Test Centre entry points; selected-pixel deep links
  preselect the Test Centre without inspecting or activating hardware. Canonical
  output previews and a saved-topology pixel endpoint resolve all cabinet addresses,
  including unallocated pixels. Drawer-relative offsets use the existing resolver's
  cumulative range offsets. GPIO/firmware identity is never inferred from drawings.
- **Persistence/security:** metadata, section transforms, pixel overrides, jumpers
  and embedded PNG/JPEG backgrounds participate in existing system-flag backup and
  restore. Strict JSON, bounded counts/coordinates/metadata, raster limits, exact
  section coverage, unique identities, role checks and CSRF protect writes.
- **Delegation/review:** bounded backend, interface and test work was delegated to
  GPT-6 Luna as requested. Worker reports were not acceptance. Lead review corrected
  incomplete section persistence/rendering, missing electrical route registration,
  ownership/dependency checks, incorrect column defaults, drawer-relative offsets,
  physical direction/numbering, custom dragging transforms and test harness issues.
- **Verification:** full Go tests and race suite, build/vet, JS tests, sqlc regeneration,
  templ generation, CSS generation, dedicated designer browser, LED Test Centre
  browser, CSRF, layout/physical-first/barcode browsers and inventory-lifecycle plus
  separate-app restore browser passed. Designer acceptance blocks external browser
  traffic, supplies local Alpine, and asserts zero controller requests/writes for
  drawing/selection. It covers pixel dragging, section transforms, rows/columns,
  raster/jumper reopening, mapping/ID preservation, stale saves, protected electrical
  edits, required confirmation, successful unmapped reversal and adding a second
  independent output. Initial race fixture collision and stale copied test selectors
  were corrected before passing reruns.
- **Operator guide:** `docs/LED_WIRING_DESIGNER.md`. Backgrounds support PNG/JPEG
  only (1 MiB, 4096px per dimension, 16MP). Large output drawings sample displayed
  pixels. Electrical remapping of already-mapped installations is deliberately
  blocked rather than inventing an automatic migration. Physical commissioning and
  independent published-source review remain separate future gates.

## Wiring Designer release candidate publication and isolated 8094 upgrade — 2026-10-17

- Owner accepts the verified implementation and explicitly authorizes a scoped commit
  in the isolated integration worktree, normal fast-forward publication to
  `origin/development/chatgpt-m6-m7`, complete image build, recoverable backup,
  copied-data acceptance and upgrade of only the isolated port-8094 installation.
  This supersedes this task's earlier no-commit/no-deployment restriction and permits
  the integration-branch source commit; no main merge or other deployment is authorized.
- Before publication, fetch verified that the remote development tip `b235bfd` is
  an ancestor of integration baseline `f546493`, with zero remote-only commits.
  Existing untracked `.live-e2e/`, `compose.led-8094.yaml`, generated test debris and
  private backup/operational files remain excluded from the feature commit.
- Fresh release gates passed: build/vet, complete race suite, JavaScript tests and
  the dedicated isolated designer browser acceptance. Previously recorded complete
  test and browser results remain applicable to the accepted source.
- Consistent pre-upgrade backup was taken with only 8094 briefly stopped, including
  SQLite/WAL consolidation, uploads, logs, previous Compose and container configuration.
  Integrity and foreign-key checks passed at migration 27; the current install has
  one account, no inventory parts and no controllers. Private recovery material is
  in `.8094-backups/20261017-pre-wiring-designer/`, outside the committed source.
- Deployment gates: prove that backup starts, test the published candidate image on
  a separate data copy, verify existing account/password/settings fingerprints,
  graphical save/reopen and app backup/restore, then refresh the live backup before
  the in-place swap. Preserve the same 8094 volumes and physical-output=false.
  Live UI validation may use a short-lived maintenance session for the existing admin
  (no password reset) and temporary offline loopback-only test records, removed after
  checking. No real controller contact is authorized. Ports 8090–8093 remain untouched.
