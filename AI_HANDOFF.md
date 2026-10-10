# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 77
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 044 — Read-only architecture and data audit for Cabinet → Drawer → Bin → Parts redesign
Production-Authorization: NONE (read-only; no production host contacted, no mutation)
Audited-Tree: origin/main @ 8922670 (roadmap merge) — handoff branch code tree is frozen at 63016f4
Result: **AUDIT COMPLETE.** Schema, paths, WLED mapping semantics, 68-vs-69 analysis, proposed schema, gap analysis and phased plan below. No code changed, no migration, no LED command, no deploy.

## 0. Scope, method and safety

- **Read-only.** No production host contacted (no `ssh`, no `docker`, no live DB).
  No SQL write, migration, file change, LED/WLED command, or Home Assistant
  operation. No secrets printed.
- **Audited tree:** `origin/main` @ `8922670c148dfde836a1fcc93b770fe6e00c6b29`
  ("docs: establish living storage roadmap (#5)"), which is the
  `Main-Branch-After-Roadmap-Merge` named in Sequence 76. Files read via
  `git show origin/main:<path>`.
- **Reference roadmap:** `ROADMAP.md` at the repo root on `main` (155 lines).
- **Tooling limit:** no Go toolchain in the audit environment (`go: not found`),
  so the Go test suite could **not** be run. All findings are from source
  inspection. This is stated so no passing-test claim is implied.
- **Baseline reuse:** production counts and the production container/controller
  rows are taken from the Sequence 27 (Task 021) read-only handoff and the
  Sequence 75 (Task 043) deployment handoff, not from a new production read.

### 0.1 Channel topology (carried over from the pre-audit check)

`experiment/ai-handoff` is a **documentation-only channel**. Its code tree is
byte-identical to the merge-base `63016f4` (`git diff 63016f4
origin/experiment/ai-handoff -- . ':(exclude)AI_HANDOFF.md' ':(exclude)handoff'`
is empty). `main` is `63016f4` + **14 commits** (13 code commits + the roadmap
merge). The branch is missing `main`'s CSRF, secure-cookie, LED-role-gating,
dashboard fixes, CI and tests. **All findings below are from `main`.**

---

## A. Schema enumeration (migrations, tables, columns, FKs, indexes)

### A.1 Migrations (`sql/schema/`, goose, current version 10)

| # | File | Effect |
| --- | --- | --- |
| 001 | `001_init.sql` | users, sessions, settings, controllers, bins, parts, parts_fts, part_ai_prompts, part_links, part_docs, part_assignments, tags, part_tags, audit_logs, inspiration_templates |
| 002 | `002_fts_triggers.sql` | FTS5 sync + tag-cache triggers |
| 003 | `003_add_seed_flag.sql` | `settings.inspiration_seeds_applied` |
| 004 | `004_multi_container_hierarchy.sql` | containers, walls, wall_cards; bins re-parented to containers; controllers lose `config_json` |
| 005 | `005_system_flags.sql` | `system_flags` |
| 006 | `006_add_container_position.sql` | `containers.position_index` |
| 007 | `007_supplier_integration.sql` | supplier_references, part_parameters, part_pricing, supplier_cache, supplier_credentials; `parts.footprint`; supplier settings |
| 008 | `008_price_history.sql` | `price_history` |
| 009 | `009_add_error_color.sql` | `settings.color_error` |
| 010 | `010_drawer_led_allocation.sql` | `containers.led_start`, `containers.led_count` |

### A.2 Storage hierarchy tables

**`controllers`** (`001_init.sql:43`, reshaped by `004`): `id`, `name`,
`ip_address`, `port`, `mac_address`, `is_online`, `led_count`, `created_at`.
This is the WLED device — the current "cabinet".

**`containers`** (`004_multi_container_hierarchy.sql`; `006`; `010`): `id`,
`name`, `controller_id` (FK → controllers, CASCADE), `segment_id` (default 0),
`config_json` (layout: `linear`/`grid`/`compound`), `created_at`, `updated_at`,
`position_index`, `led_start`, `led_count`. Index
`idx_containers_controller_id`. This is the current "drawer"/"string".

**`bins`** (`004`): `id`, `name`, `container_id` (FK → containers, CASCADE),
`led_index` (nullable), `width` (default 1), `grid_x`, `grid_y`.
`UNIQUE(container_id, led_index)`; index `idx_bins_container_id`.

**`walls`** / **`wall_cards`** (`004`): display-only grouping of containers
(`wall_cards.wall_id` → walls CASCADE, `wall_cards.container_id` → containers
CASCADE, `position_index`, `config_json`). No LED meaning.

### A.3 Parts and inventory tables

**`parts`** (`001`; `007` adds `footprint`): `id`, `name`, `description`,
`part_number`, `manufacturer`, `supplier`, `unit_cost`, `reorder_level`,
`min_stock_threshold`, `barcode_data` (UNIQUE), `image_path`, `is_favorite`,
`tags` (denormalised FTS cache), `created_at`, `updated_at`, `footprint`.

**`part_assignments`** (`001_init.sql:132`) — **the source of truth for stock**:
`id`, `part_id` (FK → parts, CASCADE), `bin_id` (nullable, FK → bins, SET NULL),
`quantity` (CHECK ≥ 0). `UNIQUE(part_id, bin_id)`; index
`idx_part_assignments_bin_id`.
**This already supports multiple part types per bin and per-location quantities**
(one row per part+bin, each with its own quantity). A `bin_id` of NULL is
"orphaned stock" (unassigned).

Related: `part_links`, `part_docs`, `part_ai_prompts`, `tags`, `part_tags`,
`audit_logs`, `inspiration_templates`, `supplier_references`, `part_parameters`,
`part_pricing`, `price_history`, `supplier_cache`, `supplier_credentials`,
`users`, `sessions`, `settings`, `system_flags`.

### A.4 FTS and flags

- `parts_fts` (FTS5, external-content over `parts`) with insert/update/delete
  triggers plus tag-cache maintenance triggers (`002_fts_triggers.sql`).
- `system_flags` (`005`): `key` PK, `value`, `updated_at`. Holds
  `led_coordinate_space` (`internal/ledspace/ledspace.go:20`),
  `migration_005_applied` (`internal/hardware/migration.go:19`),
  `drawer_allocation_backfilled` (`internal/hardware/allocation.go:463`).

### A.5 Query layer

`sqlc` v1.29.0 generates `internal/db/*.sql.go` from `sql/queries/*.sql`
(192 named queries) with prepared statements and a `Querier` interface
(`sqlc.yaml`, `internal/db/querier.go`).

---

## B. Read and write paths (what is reusable vs what must change)

### B.1 Hardware / grid (the LED-mapping editor)

- Routes (admin): `GET /hardware/{id}/grid` → `HandleHardwareGrid`;
  `POST /hardware/{id}/grid` → `HandleHardwareGridSave`; `POST /hardware`,
  `POST /hardware/import`, `GET /hardware/{id}/export`,
  `POST /hardware/{id}/delete` (`internal/router/router.go`).
- Service: `hardware.Service.SaveGridInSpace` (`internal/hardware/service.go:209`)
  parses `gridDataJSON` (bins) + `configJSON` (containers), resolves/validates
  allocations (`resolveAllocations`, `internal/hardware/allocation.go:282`),
  validates bin containment and overlaps (`validateBinMappingsInSpace`,
  `allocation.go:103`), then syncs containers and bins inside an
  `ExecImmediateTx` (`service.go:244`).
- UI: `web/components/grid_painter.templ` + `web/static/js/grid_painter.js`.
- **Reusable:** the transactional save pattern, allocation validation, the grid
  painter interaction model. **Must change:** the editor currently edits
  *bins-in-a-container*; the redesign needs *drawers-in-a-cabinet* and
  *bins-in-a-drawer* as two separate editors.

### B.2 Drawer (container) view

- Route (read): `GET /drawers/{id}` → `HandleDrawerDetail`
  (`internal/handler/drawers.go:15`); `POST /drawers/{id}/locate` →
  `HandleDrawerLocate` (`drawers.go:58`, editor/admin + CSRF on `main`).
- Page: `web/pages/drawer.templ` — header + breadcrumbs, a Locate button, an
  auto-locate on load (`hx-trigger="load"`), and a **card grid of bins** with
  their contents. **No Grid/List toggle, no flat "all parts" list, no bin-layout
  view.**
- **Reusable:** the page shell, breadcrumbs, per-bin contents
  (`GetBinContents`, `sql/queries/bins.sql:60`). **Must change:** add the
  Grid/List toggle, the flat parts list, and the bin-layout view.

### B.3 Dashboard

- Route: `GET /` → `HandleDashboard`. Service
  `internal/dashboard/service.go` (`main`): `GetStats`, `GetGrid`,
  `GetGridByController`, `GetWalls`, `GetWallWithContainers`,
  `GetAllWallsWithContainers`, wall CRUD.
- Query `GetDashboardGrid` (`sql/queries/dashboard.sql`) drives from
  `controllers` LEFT JOIN `containers` LEFT JOIN `bins` (grid-mapped only) LEFT
  JOIN `part_assignments` LEFT JOIN `parts`.
- UI: `web/pages/dashboard.templ`, `web/components/dashboard_grid.templ`,
  `dashboard_wall.templ`.
- **Reusable:** the controller-driven grid query shape and the wall grouping.
  **Must change:** the dashboard must present **selectable cabinets**, not a wall
  of stock-statistic cards.

### B.4 Parts, stock and locate

- Part detail: `GET /parts/{id}` → `HandlePartDetail`; Locate button
  `web/components/part_locate_button.templ` → `POST /parts/{id}/locate` →
  `HandlePartLocate`.
- Stock: `internal/stock/service.go` — `AssignStock` (`:60`), `AdjustStock`
  (`:112`), `MoveStock` (`:151`), `RemoveStock` (`:227`), all transactional and
  audited. Routes under `/parts/{id}/assign`, `/parts/{id}/stock/{assignment_id}/…`.
- **Reusable:** all of it. The inventory write path already models
  part↔bin↔quantity correctly.

### B.5 API / MCP

- `/api/v1` (`internal/api/`): read-only + locate (`GET /health`,
  `POST /global-off`, `POST /parts/{id}/locate`, `POST /bins/{id}/locate`,
  `GET /parts?q=`, `GET /parts/{id}`, `GET /hardware`).
- MCP (`cmd/mcp-server/tools.go`): `search_parts`, `locate_part`, `locate_bin`,
  `global_off`, `list_controllers`.
- **Reusable:** the token/auth pattern and the locate plumbing. **Must change:**
  locate semantics move from bin-level to drawer-level (see C).

---

## C. Current WLED mapping model (code evidence)

### C.1 Entities

- **Controller** = the WLED device (`controllers`: `ip_address`, `port`,
  `led_count`). One controller per cabinet today (1:1).
- **String vs segment:** WLEDger has **no separate "string" entity**. A
  container carries a `segment_id` (`containers.segment_id`), which is the WLED
  segment. In production the operator named the two containers
  `String 1 (LEDs 1-628)` (segment 0) and `String 2 (LEDs 629-1141)` (segment 1)
  — i.e. **one segment per physical string**, but the model treats the container
  as the unit, not the string.
- **Bin** = a location inside a container (`bins.led_index`, `bins.width`,
  `bins.grid_x`, `bins.grid_y`).

### C.2 LED coordinates

- **Container allocation** (`containers.led_start`, `containers.led_count`):
  segment-relative, `led_start` = first LED within the segment, `led_count` =
  number of LEDs. Validated non-negative, positive count, no overflow
  (`ValidateAllocation`, `internal/hardware/allocation.go:24`). Allocations in
  the same segment must not overlap (`ValidateAllocations`, `allocation.go:252`).
- **Bin mapping** (`bins.led_index`, `bins.width`): `led_index` is the start LED
  and `width` the span, **start-inclusive / end-exclusive**: the range is
  `[led_index, led_index+width)` (error text and checks in
  `ValidateBinMapping`, `allocation.go:42-58`). A bin must be fully contained in
  its drawer's allocation. `width < 1` is clamped to 1 (`clampWidth`,
  `internal/hardware/service.go:188`). `led_index` is **nullable** — a bin with
  no LED is representable and is exempt from containment checks.
- **Coordinate spaces** (`internal/ledspace/ledspace.go`): `segment` (bin
  `led_index` is segment-absolute) or `drawer` (bin `led_index` is relative to
  the drawer's `led_start`). **Production is `drawer`.** `unresolved` disables
  locate/flash and grid editing.
- **Global index** (`mapper.CalculateGlobalIndex`,
  `internal/hardware/mapper/mapper.go:64`): `segment` → `(segment_id, led_index)`;
  `drawer` → `(segment_id, led_start + led_index)`; `unresolved` → error.

### C.3 Multi-range support

**None.** A container has exactly **one** contiguous `(segment_id, led_start,
led_count)`; a bin has exactly **one** `(led_index, width)`. There is no table or
column for multiple ranges per drawer, and no way to express a drawer whose LEDs
span disjoint ranges or multiple strings. This is the central schema gap for the
redesign.

### C.4 Overlap constraints

- Bins within one container must not overlap — checked explicitly because
  `UNIQUE(container_id, led_index)` only rejects identical indices, not partial
  overlaps of variable-width bins (`validateBinMappingsInSpace`,
  `allocation.go:117-143`).
- Container allocations within one segment must not overlap
  (`ValidateAllocations`, `allocation.go:264-272`).
- There is **no** cross-segment overlap concept (segments are independent).

### C.5 Effect of locating

- `LocateBin` (`internal/wled/service.go:178`) resolves the global index and
  calls `triggerLocate` → `client.Apply` → `LightUp`, which POSTs
  `{"on":true,"seg":[{"id":segmentID,"i":[index, index+count, rgb]}]}`
  (`internal/wled/client.go:96-115`). WLED's individual-LED `i` API takes
  `[start, stop, color]` with **stop exclusive**, so `count` LEDs are lit —
  consistent with the `[start, end)` convention. *(Marked: verify against the
  deployed WLED firmware version.)*
- `LocateDrawer` (`service.go:232`) lights the drawer's single contiguous
  allocation `[led_start, led_start+led_count)`.
- `LocatePart` (`service.go:96`) lights **every** bin the part is assigned to
  (no location choice). All reads happen in one transaction; the WLED command is
  sent after commit.
- `GlobalOff` (`service.go:75`) sends a device-wide power-off per controller
  (`client.Clear`, `client.go:122`).

### C.6 Unknowns (marked, not assumed)

- Whether the deployed WLED firmware's `i` stop index is exclusive (assumed from
  the code's `[start,end)` convention).
- Whether WLED "strings" (physical outputs) map 1:1 to segments on the deployed
  device, or whether one string can host multiple segments.

---

## D. 68 recorded bins vs 69 reported physical units

### D.1 Verified (from Sequence 27 / 75 read-only handoffs)

- Production DB: **1 controller** (id 3, name `1`, ip `192.168.1.40`,
  `led_count` 0), **2 containers** (id 3 `String 1 (LEDs 1-628)` segment 0;
  id 4 `String 2 (LEDs 629-1141)` segment 1), **68 bins** (all mapped),
  **2 parts**, **2 part assignments**, **0 walls**. Coordinate space `drawer`.
- No third controller has ever existed as a live record or in any backup
  (Sequence 27 §3).

### D.2 Plausible explanations (unverified — no production read performed)

1. **One physical bin is unrecorded** — 68 rows vs 69 physical compartments; a
   bin was never added to the grid editor.
2. **The count mixes levels** — e.g. 68 bins + 1 controller = 69, or 68 bins + 2
   containers − 1 = 69; the user may be counting a different unit than "bins".
3. **A bin was deleted/merged** during the controller 1→2→3 churn (Sequence 27
   §3 shows two controllers created and deleted), leaving 68.
4. **The two "strings" are counted as units** — the user's mental model is
   strings/drawers, not bins.

### D.3 Recommendation

Do **not** reinterpret the 68 `bins` as physical drawers. Resolve the count with
the user (see G, decision D1) before any migration. A read-only production query
(`SELECT COUNT(*) FROM bins`, per-container bin counts, and the grid geometry)
would settle it, but requires explicit authorization.

---

## E. Proposed target entities (minimal-risk, additive)

Conceptual target (from `ROADMAP.md`): Cabinet → Drawer → Bin → Part, with
drawers owning one or more LED ranges and bins owning geometry.

### E.1 Entity mapping (reuse-first)

| Target | Proposal | Rationale |
| --- | --- | --- |
| **cabinet** | Reuse `controllers` as the cabinet (1 controller per cabinet), optionally add `cabinets` later | 1:1 today; avoids a data migration. A separate `cabinets` table is only needed if cabinets ever outnumber controllers. |
| **drawer** | Reuse `containers`; add geometry (`grid_x`, `grid_y`, `grid_w`, `grid_h`) | Containers already are the drawer/string unit and already carry `position_index`. |
| **drawer_led_range** | **New table** `drawer_led_ranges(id, drawer_id, segment_id, led_start, led_count)` | Enables multiple/disjoint ranges across strings — the key missing capability. |
| **bin** | Reuse `bins`; add geometry (`grid_w`, `grid_h` in drawer-grid units) | `bins.width` is the **LED** width, not grid width, so grid geometry must be separate. |
| **part_location** | Reuse `part_assignments` | Already supports multiple parts per bin and per-location quantities. |

### E.2 Proposed DDL (additive, nullable — no data loss)

```sql
-- 011_drawer_geometry.sql
ALTER TABLE containers ADD COLUMN grid_x INTEGER;
ALTER TABLE containers ADD COLUMN grid_y INTEGER;
ALTER TABLE containers ADD COLUMN grid_w INTEGER;
ALTER TABLE containers ADD COLUMN grid_h INTEGER;

-- 012_drawer_led_ranges.sql
CREATE TABLE drawer_led_ranges (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    drawer_id   INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    segment_id  INTEGER NOT NULL DEFAULT 0,
    led_start   INTEGER NOT NULL,
    led_count   INTEGER NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_drawer_led_ranges_drawer ON drawer_led_ranges(drawer_id);

-- 013_bin_geometry.sql
ALTER TABLE bins ADD COLUMN grid_w INTEGER;
ALTER TABLE bins ADD COLUMN grid_h INTEGER;
```

### E.3 Migration sequencing and backwards compatibility

1. **011–013 additive only** — existing `containers.segment_id/led_start/led_count`
   and `bins.led_index/width` remain authoritative and untouched.
2. **Backfill** `drawer_led_ranges` from each container's existing
   `(segment_id, led_start, led_count)` (one row per container) — idempotent.
3. **Dual-read** — new UI reads ranges from `drawer_led_ranges`; locate uses the
   union of a drawer's ranges. Legacy columns stay in sync until cutover.
4. **Cutover** — once verified, the legacy columns become read-only history.
   Dropping them is optional and non-portable on SQLite (see the note in
   `010_drawer_led_allocation.sql`), so leaving them is the safe default.

### E.4 Rollback strategy

- Additive columns and a new table are inert if unused: rollback = stop reading
  them; no data is lost. This mirrors the existing `010` rollback note.
- The mapping fingerprint protocol (Sequence 43/75) must be preserved: any
  migration that touches `controllers`/`containers`/`bins` must be preceded by a
  fingerprint snapshot and followed by an equality check.

---

## F. Gap analysis vs `ROADMAP.md` Confirmed requirements

Status: **existing** / **reusable** / **partial** / **missing** / **unknown**.

### F.1 Browse inventory

| Requirement | Status | Evidence |
| --- | --- | --- |
| Dashboard shows selectable cabinets | **partial** | `dashboard.templ` renders walls or a controller grid, not a cabinet picker |
| Cabinet page with visual drawer layout | **missing** | no cabinet page; `hardware_grid.templ` edits bins, not drawers |
| Drawer page shows all parts immediately | **partial** | `drawer.templ` shows bins+contents, not a flat parts list |
| Drawer Grid/List toggle | **missing** | `drawer.templ` has one card grid |
| Separate View Bin Layout action | **missing** | none |
| Part details page | **existing** | `part_detail.templ` |
| Breadcrumbs | **reusable** | `drawer.templ` already uses `components.PageHeader` breadcrumbs |

### F.2 Locate a part

| Requirement | Status | Evidence |
| --- | --- | --- |
| Locate button on part details | **existing** | `part_locate_button.templ`, `HandlePartLocate` |
| Resolve one/multiple locations with a choice | **partial** | `LocatePart` lights all; no choice UI |
| Illuminate all configured drawer LED ranges | **partial** | `LocateDrawer` lights one contiguous range; no multi-range |
| Bin-layout popup with matching bin highlighted | **missing** | none |
| Stop / Turn Off LEDs | **partial** | `GlobalOff` exists; no per-locate stop |
| No separate LED per bin | **partial** | bins still carry `led_index`; drawer-level LEDs are the target |

### F.3 Configure a cabinet

| Requirement | Status | Evidence |
| --- | --- | --- |
| One WLED controller per cabinet | **existing** | 1:1 controller↔cabinet |
| Any number of strings | **partial** | multiple containers per controller, each one `segment_id` |
| Cabinet face editor (place/resize drawers) | **missing** | grid painter edits bins |
| Drawer ↔ one or more LED ranges (disjoint/strings) | **missing** | single allocation per container (C.3) |
| Repositioning must not rewrite mappings | **partial** | `SaveGridInSpace` rewrites containers+bins together |

### F.4 Configure a drawer

| Requirement | Status | Evidence |
| --- | --- | --- |
| Per-drawer editable bin layout | **partial** | grid painter, but not scoped to a drawer page |
| Place/resize bins of differing shapes | **partial** | `grid_x/grid_y` only; no bin grid width/height |
| Any number of bins, multiple parts per bin | **existing** | `bins`, `part_assignments` |
| Bin layout button + Locate popup | **missing** | none |

---

## G. Phased plan, tests, acceptance criteria, and decisions needed

### G.1 Decisions requiring user input

- **D1 — the 69 units.** What are the 69 physical units (drawers, bins, or
  another count), and how do the two strings map to them? (Blocks Phase 0.)
- **D2 — cabinet identity.** Is a cabinet always exactly one controller, or can
  it be a separate entity? (Determines whether to reuse `controllers`.)
- **D3 — overlapping LED ranges.** Disallow, warn, or allow? (Determines
  validation in Phase 2.)
- **D4 — multi-location locate UX.** How should Locate behave for a part in
  several bins? (Phase 4.)
- **D5 — auto-off.** Should locate LEDs turn off after a configurable timeout?
  (Phase 4; `settings.enable_locate_timeout` already exists.)
- **D6 — bin geometry.** Fixed grid, adjustable grid, or free positioning?
  (Phase 3.)
- **D7 — multi-location quantities.** Already supported by `part_assignments`
  (A.3) — confirm no migration is wanted.

### G.2 Implementation decisions (no user input needed)

- Reuse `containers` as drawers and `part_assignments` as part locations.
- Add `drawer_led_ranges`; keep legacy columns for compatibility.
- Additive, nullable migrations only; preserve the mapping fingerprint protocol.

### G.3 Phases (aligned to `ROADMAP.md`)

- **Phase 0 — baseline/design:** resolve D1; freeze the fingerprint; finalize
  the migration/rollback strategy. *(This audit is the Phase 0 read-only part.)*
- **Phase 1 — cabinet model + dashboard:** cabinet picker; cabinet page with a
  read-only drawer layout; breadcrumbs.
- **Phase 2 — cabinet drawer-layout editor:** add/move/resize/label/order
  drawers; map drawers to one or more LED ranges; validate overlaps/bounds.
- **Phase 3 — drawer bins + contents:** per-drawer bin geometry; flat parts list
  with Grid/List toggle; View Bin Layout; part-details navigation.
- **Phase 4 — locate workflow:** drawer-level locate across all ranges; bin-layout
  popup with highlight; stop/timeout/offline/error handling.
- **Phase 5 — refinement/validation:** accessibility, undo, templates,
  integration/migration/regression/e2e tests, staged deployment with backup and
  rollback.

### G.4 Test plan and acceptance criteria (per phase)

- **Schema:** migration up/down idempotency; backfill correctness (one range per
  legacy container); fingerprint unchanged after additive migrations.
- **Validation:** reject overlapping ranges (per D3); reject out-of-bounds bins;
  reject cross-controller ranges.
- **Locate:** exactly the configured ranges light; no unrelated LEDs; stop works;
  offline controller handled; unresolved space still blocks locate.
- **UI:** drawer page lists all parts immediately; Grid/List toggle; bin-layout
  highlight matches the located bin; breadcrumbs navigate back.
- **Regression:** existing 68-bin mapping still locates identically (fingerprint
  + a locate smoke test).

### G.5 Recommended smallest first implementation slice

**Phase 1, read-only cabinet page + drawer layout rendering**, backed by the
additive `011`/`012` migrations and the `drawer_led_ranges` backfill — no write
paths, no locate change, no production migration until separately authorized.

---

## Evidence

- Audited commit: `origin/main` @ `8922670c148dfde836a1fcc93b770fe6e00c6b29`
- Merge-base: `63016f4e75134ae0675934818fb77f53dd463800`
- Branch tip: `feabec9` (Sequence 76); branch code tree == merge-base (verified)
- `main` = merge-base + 14 commits; branch = merge-base + 76 docs-only commits
- Production baseline: Sequence 27 (Task 021) and Sequence 75 (Task 043) handoffs
- No production host contacted; no mutation; Go tests not run (no toolchain)

**STOP — awaiting review.**
