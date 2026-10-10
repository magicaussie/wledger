# WLEDger — Product Roadmap

Last updated: 2026-10-11
Status: Living design document — requirements discussion, not implementation authorization
Owner: WLEDger project
Working branch: `docs/storage-roadmap`

## Purpose

Capture agreed requirements, future ideas, decisions, implementation phases, and verification criteria so the project can continue across AI sessions without losing context.

**Status labels:** Confirmed = explicitly agreed; Proposed = suggestion awaiting agreement; Open = decision required; Planned = not implemented; In progress = active work; Done = implemented and verified.

## Product vision — Confirmed

WLEDger is a visual inventory and location system for physical storage cabinets. It must represent real cabinet and drawer geometry, show inventory with images, and guide the user to a part by lighting the relevant physical drawer and highlighting its bin on screen.

Hierarchy:

```text
Dashboard
  └── Cabinet (one assigned WLED controller; one or more strings)
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

- Assign **one WLED controller per cabinet**.
- A cabinet can use **any number of strings** from that controller.
- Design the physical cabinet face with drawers of different sizes and positions.
- Grid-based editor with placement and resizing; match the real-world layout.
- Associate each drawer with **one or more LED ranges**, potentially non-contiguous and across strings on the cabinet's controller.
- Visual repositioning must **not silently rewrite hardware mappings**.

### 4. Configure a drawer

- Each drawer has its **own editable internal bin layout**.
- Place and resize bins of differing shapes/sizes to match real compartments.
- Support any number of bins and multiple parts per bin.
- Bin layout is available via a separate button on the drawer page and as the highlighted popup in Locate.

## Data model direction — Proposed, not yet finalized

- `cabinet`: name, display order, assigned WLED controller, layout settings.
- `drawer`: cabinet reference, label, geometry and ordering.
- `drawer_led_range`: drawer reference, string/segment identifier, start/end LED indices; multiple rows per drawer.
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
- [ ] Create/associate cabinets with one WLED controller and multiple strings.
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

1. What exactly are the **69** physical units (drawers, existing bins, or another count), and how do the two existing strings map to them?
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
