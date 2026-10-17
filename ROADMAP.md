> **HISTORICAL / IMPLEMENTATION TRACKING — NOT AUTHORITATIVE TARGET DESIGN (2026-10-17).**
> Owner-approved target requirements are in [docs/PRODUCT_SPEC.md](docs/PRODUCT_SPEC.md). Conflicting statements below (including one-controller cabinets, bin-owned LEDs, multiple drawer pixel ranges, and QR-only label generation) are superseded. Do not use this file as instructions for new architecture. Check source/tests for actual implemented status; older completion claims may refer to a different commit.

# WLEDger — Product Roadmap

Last updated: 2026-10-12
Status: Living product requirements and implementation record
Owner: WLEDger project
Working branch: `development/chatgpt-m6-m7`

## Purpose

Capture agreed requirements, future ideas, decisions, implementation phases, and verification criteria so the project can continue across AI sessions without losing context.

**Status labels:** Confirmed = explicitly agreed; Proposed = suggestion awaiting agreement; Open = decision required; Planned = not implemented; In progress = active work; Done = implemented and verified.

## Product vision — Confirmed

WLEDger is a visual inventory and location system for physical storage cabinets. It must represent real cabinet and drawer geometry, show inventory with images, and guide the user to a part by lighting the relevant physical drawer and highlighting its bin on screen.

Hierarchy:

```text
Dashboard
  └── Cabinet (up to five independently assigned WLED controllers)
        └── Drawer (position/size in cabinet; one or more LED ranges)
              └── Bin (position/size in drawer; no individual LEDs required)
                    └── Part inventory (multiple part types per bin)
```

A part type may be stocked in multiple bins, with quantities tracked per location. A bin may contain multiple different part types.

## Confirmed user journeys

### 1. Browse inventory

1. Dashboard displays selectable cabinets, not a wall of stock-statistic cards.
2. Select a cabinet to open its own page showing a visual representation of the cabinet's physical drawer layout.
3. Select a drawer to open a page showing **all parts in that drawer immediately**.
4. Drawer contents support a **Grid/List toggle** and a separate **View Bin Layout** action.
5. Select a part to open its existing part-details page.
6. Use breadcrumbs or equivalent navigation to move back through cabinet and drawer.

### 2. Locate a part

1. On the part-details page, press **Locate**.
2. Determine the part's bin, drawer and cabinet. If stocked in multiple locations, offer a location choice (proposed interaction; confirm UX).
3. Activate the drawer's configured LED range(s) on the assigned WLED controller.
4. Display a popup of that drawer's saved **bin layout**, highlighting the exact bin containing the part.
5. Provide **Stop / Turn Off LEDs** and safe cleanup/error handling.
6. **Do not** require a separate LED per bin: drawer LEDs identify the physical drawer; the popup identifies the internal bin.

### 3. Configure a cabinet

- Assign up to **five WLED controllers per physical cabinet**; each controller belongs to at most one cabinet.
- Each controller can drive any number of physical LED outputs/strings allowed by its WLED hardware and firmware.
- Design the physical cabinet face with drawers of different sizes and positions.
- Grid-based editor with placement and resizing; match the real-world layout.
- Associate each drawer with one selected cabinet controller and **one or more non-contiguous ranges** in that controller's continuous 1-based LED space; ranges may cross that controller's physical outputs.
- Visual repositioning must **not silently rewrite hardware mappings**.

### 4. Configure a drawer

- Each drawer has its **own editable internal bin layout**.
- Place and resize bins of differing shapes/sizes to match real compartments.
- Support any number of bins and multiple parts per bin.
- Bin layout is available via a separate button on the drawer page and as the highlighted popup in Locate.

## Data model direction — Proposed, not yet finalized

- `cabinet`: name, display order, layout settings; controller ownership is a separate ordered membership relation (maximum five).
- `drawer`: cabinet reference, label, geometry and ordering.
- `drawer_led_allocation`: drawer, cabinet, and controller references plus 1-based controller-wide start/count and order; multiple rows per drawer, all on one controller.
- `bin`: drawer reference, label, geometry and ordering.
- `part`: existing part catalogue record.
- `part_location` / inventory assignment: part reference, bin reference, quantity, and existing inventory metadata.

**Important:** These are conceptual entities, not claims about the current schema. Inspect existing controller/container/bin/part tables and API/UI paths before selecting migrations. Preserve current inventory and mappings. Clarify WLED's actual segment/string/index semantics before implementing range assignment.

## Existing-system considerations

- Existing production database was reported as **1 controller, 2 containers, 68 bins, 2 parts and 2 part assignments** at the last verified deployment. User reports **69 physical bins/drawers**; the meaning and discrepancy are **unresolved**. Do not equate existing database bins with new physical drawers until audited.
- Existing Wall dashboard does not implement the requested hierarchy or editors. Reuse suitable underlying functionality, but do not treat the current Wall design as the target.
- Production mainserver is **192.168.1.108**; the workstation has a separate unrelated WLEDger Docker instance. Always verify host and Docker context.
- Preserve controller/container/bin mappings, existing part records, user data, and existing production state. **No production migration, LED activation, or deployment without explicit authorization.**

## Suggested implementation phases — Planned

### Phase 0 — Baseline and design
- [ ] Read-only audit of current schema, API routes, UI components and WLED mapping semantics.
- [ ] Reconcile the 69-physical-versus-68-record count and distinguish drawers from bins.
- [ ] Finalize migration and compatibility strategy, including rollback and preservation of existing data.
- [ ] Define UI behavior for multiple part locations and missing/offline controllers.
- [ ] Write acceptance criteria and tests for each phase.

### Phase 1 — Cabinet model and dashboard
- [ ] Create/associate cabinets with up to five WLED controllers and controller-specific physical outputs.
- [ ] Dashboard cabinet picker.
- [ ] Cabinet page with visual drawer layout (read-only first).
- [ ] Navigation and breadcrumbs.

### Phase 2 — Cabinet drawer-layout editor
- [ ] Add, move, resize, label and order drawers.
- [ ] Configurable grid snapping and layout persistence.
- [ ] Map each drawer to one or more LED ranges, including disjoint ranges/strings.
- [ ] Validate overlaps, bounds and controller consistency; prevent accidental mapping changes.

### Phase 3 — Drawer bins and contents
- [ ] Add/edit/save bin geometry inside each drawer.
- [ ] Associate existing inventory bins and part assignments without data loss.
- [ ] Drawer page shows all parts immediately, with Grid/List toggle and images.
- [ ] Separate View Bin Layout action.
- [ ] Part-details navigation.

### Phase 4 — Locate workflow
- [ ] Locate button on part-details page.
- [ ] Resolve one or multiple stock locations.
- [ ] Illuminate all configured LED ranges for selected drawer.
- [ ] Show bin-layout popup with the matching bin highlighted.
- [ ] Stop LEDs; handle timeout, controller offline, errors and repeated locate actions.
- [ ] Test no unrelated LEDs activate.

### Phase 5 — Refinement and validation
- [ ] Editor usability, keyboard/touch accessibility and responsive layout.
- [ ] Undo/revert, previews and layout templates (proposed).
- [ ] Integration, migration, regression and end-to-end tests.
- [ ] Staged deployment plan, backup, rollback and explicit production approval.

## Open design questions

1. What exactly are the **69** physical units (drawers, existing bins, or another count), and how do the existing physical outputs map to them? Physical outputs must not be conflated with WLED segments.
2. Should cabinet/drawer layout editing be available only to administrators?
3. How should overlapping LED ranges across drawers be handled: disallow, warn, or explicitly allow?
4. How should the Locate popup behave for a part stocked in more than one bin?
5. Should Locate LEDs turn off automatically after a configurable timeout?
6. Should bin geometry use a fixed grid, adjustable grid, or optional free positioning?
7. Do parts already support multiple location-specific quantities in the current schema, or is a migration needed?

## Ideas backlog — Not yet approved

- Cabinet and drawer layout templates.
- Import wizard to bootstrap layouts from existing mappings.
- Drag-and-drop editor with undo/redo.
- Search and filters on drawer contents.
- Printable bin/drawer labels or QR codes.
- Mobile-friendly locate workflow.

## Change log

- **2026-10-11 — v0.1:** Captured confirmed Cabinet → Drawer → Bin → Parts model; cabinet and drawer editors; drawer parts Grid/List default; separate bin layout; part-details Locate with drawer LEDs and highlighted bin; multi-range LED support; initial phased plan and unresolved questions.


## How to maintain this roadmap

- Record new agreed requirements as **Confirmed**, with date and acceptance criteria.
- Keep speculative features under **Ideas backlog** until the user approves them.
- Move work through Planned → In progress → Done only with evidence of implementation and tests.
- Record major design changes and migrations in the change log.
- Keep this document on a review branch until explicitly merged; do not deploy from it.


## Design update — 2026-10-11: cabinet editor owns drawer and LED configuration

**Confirmed by user:**
- Physical cabinet contains **exactly 69 drawers**. Existing production database reports **68 mapped bin records**; discrepancy remains to be audited. Do not treat the 68 records as definitive physical drawer count or silently invent the 69th mapping.
- The **cabinet drawer-layout editor is the primary interface** for adding drawers and assigning each drawer's LED range(s).
- At **any time after initial setup**, the user must be able to add/remove/reposition/resize/renumber drawers and modify their LED assignments or assigned LED counts.
- Each drawer may have multiple ranges across strings/segments, as previously agreed.
- Moving or renumbering a drawer must preserve its stable identity, bins, parts and quantities; changing LED assignments must not mutate stock.

**Design safeguards proposed for review:**
- Separate immutable drawer IDs from user-facing drawer numbers/order.
- Validate LED range bounds and overlaps before committing changes; provide clear conflict feedback.
- Removing drawers with bins/stock must require an explicit safe workflow (move inventory, archive, or cancel); never silently cascade-delete stock.
- Keep layout geometry and electrical mappings as distinct persisted fields, even when edited in the same screen.
- Support reconfiguration after initial setup; no one-time-only import/setup workflow.
- Preserve existing legacy mapping records until an audited migration plan is approved.

**Next audit/design action:** revise the Task 044 schema proposal: the existing `containers` represent the two strings/segments, not the 69 physical drawers. Introduce stable drawer entities and drawer-to-LED-range mapping without renaming the two existing strings into drawers. Define migration from legacy bin-to-LED assignments to physical drawers only after confirming which records correspond to each of the 69 drawers.

**Roadmap changelog:** v0.2 — confirmed 69 physical drawers and ongoing editable drawer+LED configuration.


## Design update — 2026-10-11: LED assignment assistance and flexible wiring paths

**Confirmed:**
- Adding a drawer should offer an **automatic suggestion for the next available LEDs**, while allowing full manual adjustment.
- The user can shift the suggested LED range forward/backward along the string, adjust the LED count, choose another string/segment, enter a start index, or add further ranges.
- The cabinet editor must let users describe **how the physical LED string runs**; no hardcoded left-to-right assumption.
- Supported routing must include horizontal and vertical traversal; either starting direction; alternating snake/serpentine rows or columns; same-direction passes (left→right repeatedly, right→left repeatedly, top→bottom repeatedly, bottom→top repeatedly); and arbitrary custom paths.
- The wiring configuration and drawer assignments remain editable after initial setup.

**Design proposal / acceptance criteria:**
- Per string/segment: select start corner, horizontal/vertical primary axis, first-pass direction, snake vs same-direction, and orientation; show physical LED numbering preview.
- Offer a custom ordered path for nonstandard routing, discontinuities, skipped LEDs and mixed orientations; do not require one universal pattern per cabinet.
- Render actual LED numbers and direction indicators on the layout, and show unassigned LEDs, overlaps and out-of-bounds ranges before save.
- Suggest free ranges based on wiring order and current assignments, without modifying existing allocations automatically.
- Range controls: shift start by ±1/±N, adjust count, enter exact start/end, add/remove multiple ranges, and undo/cancel before commit.
- Keep electrical coordinates (controller, segment, start, count) separate from physical path/geometry; changing wiring visualization must not silently remap existing drawer LED assignments.
- Provide an explicitly initiated **Test Wiring** mode to illuminate individual LEDs/ranges sequentially, with Stop, timeout, permission checks and offline/error handling. Never activate LEDs merely by opening an editor.
- Check WLED segment/physical-string semantics and segment-relative vs controller-global coordinates before choosing persisted path schema. Irregular wiring may require per-LED physical coordinates, not just a snake preset.

**Roadmap changelog:** v0.3 — auto-suggested, adjustable LED assignments and flexible string wiring topology.


## Design update — 2026-10-11: LED mounting independent of drawer geometry

**Confirmed:** In the user's installation, LEDs are mounted to the **fixed cabinet frame**, immediately beside or above each drawer. WLEDger must also support other installations with LEDs attached to drawers or mounted behind clear/translucent drawers.

**Required capabilities:**
- LED mounting is **not** assumed to be above the drawer or fixed to the frame.
- Model physical drawer geometry, LED physical placement/mounting, and electrical LED assignments as **separate concepts**.
- Support common placements: above, below, left, right, behind/backlit, on/inside the moving drawer, perimeter, and custom; allow multiple placements per drawer where necessary.
- Store whether a mounting point belongs to the fixed cabinet frame or the moving drawer; mounting choice must not silently change drawer identity, inventory, or assigned LED indices.
- The visual editor should show mounting position and illuminated drawer association without assuming that physical LED traversal order equals drawer display order.
- Custom physical placement of LEDs may be needed for irregular installations; keep schema extensible rather than restricting to a single side enum.

**Roadmap changelog:** v0.4 — flexible frame/drawer/backlit LED mounting and separation of physical placement from electrical mapping.


## Design update — 2026-10-11: general-purpose, data-driven storage schema

**Confirmed:** The product is for many users with different cabinet and drawer layouts. The user's 69-drawer installation is **one instance**, never a fixed schema limit, default shape, or hardcoded topology.

**Database architecture requirements:**
- Arbitrary numbers of cabinets, drawers per cabinet, bins per drawer, and parts per bin, subject only to reasonable resource/validation limits.
- Independently configurable drawer and bin geometry (positions, dimensions, layout coordinate system); no fixed 69 rows, fixed columns, or predetermined drawer counts.
- Up to five exclusive WLED controller memberships per cabinet. Each physical drawer selects exactly one of its cabinet's controllers and may have multiple non-contiguous ranges on that controller.
- Flexible per-string routing/topology and per-LED physical placement as needed; allow different wiring patterns and mounting types across installations.
- Stable IDs separate from editable drawer/bin labels, numbering, ordering, geometry and LED mapping.
- Editable at any time, with safe stock relocation/archive workflows and transactional validation; no destructive cascading of inventory as a side effect of layout edits.
- Migration from legacy production records must be evidence-based; preserve current two string containers and existing 68 mapped bin records until they can be reconciled with the user's 69 physical drawers.
- Distinguish electrical string/segment, physical drawer, internal bin and inventory assignment as separate entities in the revised schema; avoid repurposing existing `containers` into physical drawers by name alone.

**Roadmap changelog:** v0.5 — flexible multi-user data-driven schema, no cabinet-specific hardcoding.


## Design update — 2026-10-12: permanent controller-wide drawer LED allocations

**Confirmed implementation contract:**
- Permanent drawer assignments are separate child records; never repurpose
  `containers.led_start/led_count` or `bins.led_index` as drawer allocations.
- User-facing indices are 1-based controller-wide pixel numbers. For physical
  strings arranged as 1–100 and 101–200, a drawer range Start 97 / Count 8 is
  one valid 97–104 allocation, even though it crosses the physical-string edge.
- Multiple non-contiguous ranges per drawer are supported and deterministically
  ordered. Ranges overlap neither other drawers nor another range on that drawer.
- Capacity and overlap are checked independently per assigned controller. Each controller has its own 1-based continuous pixel space; numbering restarts at 1 for every controller. A drawer's multiple ranges all target one selected controller and may cross that controller's physical-output boundaries. Physical outputs/strings, WLED segments and legacy containers remain distinct; no output-to-segment identity is inferred.
- Physical drawer layout saves and electrical allocation saves are separate
  transactions. A drawer needs a stable persisted ID before allocation. Moving
  or resizing the physical drawer does not rewrite its mapping.
- Allocation writes must be authorized, CSRF-protected, transactional and
  revision-safe. Backups preserve allocations; older snapshots lacking this
  additive data must not silently erase allocations on restore.
- This data model is configuration only. It does not activate hardware, alter
  existing live mappings, or implement the Locate-to-WLED conversion.

**Architecture note:** Existing `containers` rows carry `segment_id` and legacy
segment-scoped allocations, but do not identify physical strings/outputs or
controller-wide output offsets. Those records cannot establish physical-output
versus-WLED-segment correspondence. Locate requires explicit per-controller
output discovery and topology attestation; physical output boundaries are not
WLED segment boundaries, and neither containers nor output order may be used to
infer segment identity.

**Roadmap changelog:** v0.6 — confirmed persistent controller-wide drawer
allocation coordinates, cross-string range behavior, and separation from legacy
segment mappings.


## Implementation update — 2026-10-12: drawer LED Locate and hardware control

**Status:** Implementation and requested mock-only Go/JavaScript/Chromium
acceptance gates pass on `development/chatgpt-m6-m7`. This update supersedes the
earlier statement above that Locate conversion was not implemented, but it does
not authorize production operation.

- Locate starts from a selected current part/bin assignment, follows the explicit
  physical bin attachment to drawer/cabinet/controller, and reads only that
  drawer's saved controller-wide LED ranges. Missing physical association,
  missing allocation, or missing controller fails before a pixel write.
- The conversion is controller-global 1-based to WLED segment-relative 0-based.
  Each saved range is intersected with verified WLED segment bounds; ranges are
  split only at those bounds. For the two-100-pixel-string example, 97–104 is
  emitted as segment 10 `[96,100)` plus segment 20 `[0,4)` if current WLED
  segments exactly cover `[0,200)` with those bounds.
- Admins explicitly attest each controller's linear physical output order and
  reserve its saved drawer ranges exclusively for WLEDger Locate. The admin
  confirmation reads WLED `/json/info` and `/json/cfg` and saves a fingerprint
  of controller identity and explicit contiguous output starts, lengths, types,
  pins and ordering. Every Locate rereads `/json/info`,
  `/json/cfg`, and `/json/state`; missing output starts/pins or a changed
  fingerprint disables activation until re-attested. Runtime checks also reject
  capacity mismatch, RGBW, matrix/custom maps, reversal/skips, unsupported
  segment offsets, incomplete/overlapping segment coverage, non-solid target
  effects, grouping/spacing/reversal, and controller-off state. Controllers
  default to disabled.
- State writes use only the JSON segment `i` range command, e.g.
  `{"seg":[{"id":10,"i":[96,100,[R,G,B]]}]}`. No permanent per-drawer WLED
  segments, global power toggle, user effect writes, or continuous pixel arrays
  are used. Stop sends black only over the application-owned drawer ranges and
  is not a global power-off command. Prior per-pixel overrides are not restored;
  those ranges must be reserved for WLEDger Locate.
- The active app-owned session is persisted, owned by the initiating user, and
  records ranges, bin/drawer/controller, color/brightness, state and last
  acknowledged WLED command. Drawer switching first confirms range-off for the
  previous session. Stop is idempotent; timeout/restart cleanup retries
  conservatively.
  If topology or controller identity changed, cleanup is withheld and the session
  remains visibly partial instead of claiming the LEDs are off. A process-wide
  write lease and controller identity/endpoint gates serialize app writes across
  all WLED client instances. While any physical session is active, all legacy
  Locate, FlashError, GlobalOff, raw JSON state writes, and delayed auto-off writes
  fail with a conflict; the lease remains reserved through confirmed cleanup.
  The lease spans each legacy HTTP request/flash sequence, so writes across
  controllers serialize and slow requests can delay a Locate start. FlashError
  completes synchronously and propagates controller failures.
  GlobalOff is an explicit device-wide power-off that may turn off unrelated LEDs
  when no physical session is active; it reports failures synchronously and may
  partially affect other controllers if a physical session starts during its
  multi-controller run. This arbitration is process-local only: operate one
  WLEDger hardware-control process per controller/database while physical Locate
  is enabled; cross-process/external-writer arbitration is not implemented. A
  stale/offline `partial` session retains the process-wide lease and retries
  cleanup every 15 seconds without a retry cap, so legacy hardware writes remain
  unavailable until cleanup is confirmed or the stale session is explicitly
  resolved.
- The inventory detail view offers Locate for each stocked bin; the drawer view
  opens with the selected bin layout highlighted and exposes per-bin Locate and
  Stop/Turn Off. Hardware actions use editor/admin authorization and CSRF; mapping
  attestation is admin-only and CSRF-protected.
- Tests use mocked WLED HTTP endpoints only. Go 1.25.5 is available from both
  workstation toolchain paths; full `CGO_ENABLED=1 go test -tags fts5 -count=1
  ./...`, `go vet`, and `go build` pass. `npm test` passes all 20 JavaScript
  behavior tests. `npm run test:layout-browser` passes on a disposable database
  and mock controller: normal editing emitted zero WLED requests; the browser
  verified selected part/bin locations in two drawers, the 97–104 cross-segment
  payload and a second range, exact range-off Stop, and a no-association error
  with no pixel write. It reported zero page/runtime errors; its sole expected
  browser warning was the deliberately exercised HTTP 409. `sqlc generate`,
  `templ generate`, and `git diff --check` pass. No real WLED hardware was
  contacted. Independent published-source review remains required before
  acceptance; production activation is still unauthorized.

  ## Implementation update — 2026-10-12: multi-controller cabinet architecture

  **Status:** Implemented on `development/chatgpt-m6-m7`; mock-only verification is recorded in `docs/OMO_HANDOVER.md`. This does not authorize production migration, controller activation, or deployment.

  - Migration `018_cabinet_controllers.sql` adds ordered cabinet/controller memberships (slots 0–4), backfills one slot-0 membership from every existing non-NULL `cabinets.controller_id`, and leaves that legacy primary pointer and all drawer/bin/inventory identities intact. It rebuilds only `drawer_led_allocations` to reference membership pairs while preserving row IDs, ranges, order, timestamps and AUTOINCREMENT high-water mark. Schema constraints enforce exclusive controller ownership, at most five memberships, one controller per drawer, and non-overlap within each controller's global pixel space.
  - The Designer assigns up to five controllers, sets capacity per controller, selects one assigned controller per drawer, and saves that drawer's disjoint controller-wide ranges separately from physical layout. Config edits do not activate hardware. Read-only discovery explicitly fetches `/json/info` and `/json/cfg` for an assigned controller and visualizes output order/start/length/GPIO, continuous 1-based numbering, and range intersections; it neither reads segments nor writes WLED state.
  - Locate resolves the controller from the drawer's saved allocations rather than the cabinet's single legacy pointer. A session remains bound to that controller across Locate and Stop. Configuration writes and backup restore serialize against active physical sessions; changed or unsafe topology continues to fail closed. Physical outputs are never assumed to be WLED segments.
  - New backups declare memberships. Older v2 archives without the membership list retain single-controller meaning by deriving only the existing `cabinets.controller_id`; restore is refused if that would discard a secondary membership. No legacy container or bin LED index is converted.
  - Remaining hardware limitation: Locate is available only for explicitly attested WLED configurations whose output layout passes the current safe linear-topology checks. Real hardware topology was not probed. Cross-process/external WLED writer arbitration remains outside the process-local lease.

  **Roadmap changelog:** v0.7 — five-controller cabinet membership, per-controller drawer ranges, explicit output discovery and multi-controller Locate routing.


## Confirmed design update — Cabinet wiring and cabinet-wide pixels

**Status:** Confirmed target architecture; implementation in progress on `development/chatgpt-m6-m7`. This update supersedes v0.6/v0.7 statements that cabinet pixel numbers restart on each controller and that one drawer must map to one controller.

- Cabinet drawer geometry/numbering, physical LED wiring, and drawer-to-pixel allocation remain separate persisted concepts. Drawer order is never used to infer LED order.
- A cabinet's ordered controller memberships define one continuous 1-based cabinet pixel namespace. Ordered physical strings within each controller define the next sequence; string lengths determine configured controller totals. WLEDger derives controller offsets, string offsets, starts, ends and total capacity. Existing installations without explicit strings retain their pre-existing controller capacity as a compatibility fallback until strings are explicitly configured.
- Physical strings may span rows, reverse direction, zigzag, turn corners or use irregular paths. Each string supports multiple ordered path sections with pixel counts and ordered cabinet-coordinate polylines. A path marked complete must account for exactly the string's configured pixel count. Physical coordinates never alter logical numbering.
- Drawer allocations are cabinet-wide start/count ranges, support multiple non-contiguous ranges and may cross string and controller boundaries. Overlap and total-capacity checks run in cabinet coordinates. Editing controller order, string lengths or physical path geometry presents affected drawer mappings and requires explicit confirmation where destinations can change.
- The Hardware journey leads with Cabinets → Create Cabinet → Controllers → Strings → Wiring Layout → Drawer Layout → Drawer LEDs. Legacy electrical containers, segment assignments and bin LED indices remain preserved and available as distinct advanced functions, never prerequisites for a cabinet.
- Locate resolves every cabinet-wide range into controller-local 1-based spans, then reuses the verified WLED output/segment safety mapper. All targets are validated before activation; one durable logical session records every affected controller and its cleanup commands. Controller locks are acquired by sorted ID. Partial network activation is not atomic; cleanup is attempted for every controller that may have received a command, and only those pixels are targeted. The process-local lease limitation remains.
- Current backups include ordered memberships, strings, path sections and cabinet-wide ranges. Existing current-format controller-local allocation archives are converted using archived slot order and controller capacities; ambiguous or invalid ranges are rejected before restore mutation. Restores remain blocked during a potentially active Locate session.
- WLEDger string definitions express intended physical wiring; they do not configure WLED. Hardware discovery remains read-only, and activation still requires per-controller verification of `/json/info`, `/json/cfg` and safe segment topology. Physical outputs, logical strings, WLED segments and cabinet pixels are distinct.
