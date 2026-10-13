# Task 059 — UI-Only Fresh Installation Acceptance

- **Date:** 2026-10-13
- **Repository:** `magicaussie/wledger`
- **Baseline:** `development/chatgpt-m6-m7` at `4a315dfa0ed4590fd308350074761532423f91c1`
- **Overall result:** **PARTIAL — strict acceptance answer: NO.** The complete fresh-install happy path requested in the final question was exercised through the browser, including restart and mock Locate/Stop. However, the full checklist did not pass: controller settings have no edit UI, and an out-of-range controller port is accepted and persisted. See the P2 findings below.
- **Application code:** unchanged. The only code addition is the disposable UI-only browser acceptance harness `web/tests/ui_only_install.browser.test.js`.

## Final acceptance answer

**NO — not a full Task 059 pass.** A fresh user was able to create five controllers, configure a 1000-pixel cabinet, drawers, bins, wiring, allocations and inventory through browser forms, restart the app and complete mock Locate/Stop. No application records were inserted directly through SQLite.

The strict Task 059 acceptance still fails because the requested controller edit workflow is absent and the UI accepts port `70000` instead of rejecting invalid controller configuration. These defects do not prevent the tested happy-path setup when correct controller endpoints are entered initially, but they fail the explicit controller-management/validation acceptance steps and make correction of a mistaken saved endpoint awkward.

## Environment, preservation and method

| Item | Status | Evidence |
|---|---|---|
| Branch / baseline | **PASS** | Started on `development/chatgpt-m6-m7`, `HEAD=4a315dfa0ed4590fd308350074761532423f91c1`. |
| Existing dirty state | **PASS — preserved** | Starting worktree already contained modified `docs/OMO_HANDOVER.md`, untracked `.task055b-staging/`, and untracked `docs/TASK056_FUNCTIONAL_AUDIT.md`. None was reverted, opened as a test DB, staged or deleted. |
| Application/database | **PASS** | `web/tests/ui_only_install.browser.test.js` builds a disposable server in a new OS temp directory and uses its newly migrated `data/wledger.db`. It removes the temp directory on exit. |
| Browser and mocks | **PASS** | Playwright uses Chromium. Five independent WLED mocks listen on ephemeral `127.0.0.1` ports. Mock `/json/info`, `/json/cfg`, `/json/state` and writes are recorded. |
| SQL policy | **PASS** | The harness contains no SQL writes. `readOnlyRows` opens SQLite with `mode=ro` and `PRAGMA query_only=ON`; it is used only for counts and post-operation persistence assertions. All create/update/delete requests are browser form submissions. |
| Prohibited systems | **PASS** | No access to `.task055b-staging/` or port 8091; no request to port 8090; no production service/database, physical WLED, deployment or real inventory was used. |
| Initial emptiness | **PASS** | After browser setup and before controller/cabinet/inventory creation, read-only counts were zero for `controllers`, `cabinets`, `drawers`, `bins`, `parts`, `part_assignments`, `cabinet_led_strings`, `cabinet_led_path_sections` and `drawer_led_allocations`. The setup administrator is the expected first account. |

The complete browser procedure is in `web/tests/ui_only_install.browser.test.js`. It prints a JSON evidence record and marks controller-edit and invalid-port findings without modifying application source.

## Browser workflow matrix

**PASS** means the stated UI workflow was exercised and verified. **FAIL** is a reproducible acceptance defect. **BLOCKED** means a prerequisite prevented an otherwise safe test. **NOT TESTED** means it was not exercised. All rows in this table are browser evidence unless explicitly marked source review.

| Workflow | Status | Result |
|---|---|---|
| Fresh install, Goose migrations and initial admin | **PASS** | New temp DB; application startup applied the embedded schema; setup completed in browser; initial application tables listed above were empty. The version row itself was not separately queried. |
| Initial cabinet/controller/inventory navigation | **PASS** | Hardware page showed no cabinet and no controller records; physical storage showed no cabinets; all table counts were zero. Add WLED Controller and Create Cabinet controls were available. |
| Create five mock controller records | **PASS** | All five records were submitted through `/hardware` with distinct mock loopback ports; IDs/names/IPs/ports were queried read-only after redirects and persisted across restart. |
| Controller pixel capacities | **PASS** | Five controller records initially had `led_count=0`. Configuring/saving one physical string per controller through the cabinet wiring UI set the persisted capacities to 200/250/150/300/100. This is the current UI path for capacity; there is no standalone controller edit form. |
| Edit a saved controller | **FAIL — P2** | On `/hardware`, the created-controller cards exposed Configure, Export and Delete but no edit action/form. Source review found no browser update route. See finding 1. |
| Reject invalid controller settings | **FAIL — P2** | Submitting loopback IP with port `70000` via the Add WLED Controller form succeeded and created a controller row. The probe was then deleted through the normal browser Delete form. See finding 2. |
| Create cabinet and configure dimensions | **PASS** | Cabinet was created via `/storage/cabinets`, then canvas width/height were changed to 200×120 and saved through the graphical editor. |
| Create/configure five drawers | **PASS** | Five 25×18 drawers at distinct positions were drawn/numerically set in the editor, saved and read back with IDs and coordinates. |
| Create ten inventory bins | **PASS, with legacy-tool caveat** | The browser created ten bins through Controller 1’s advanced legacy grid form (2×5 grid, Auto Map, Save). No SQL was used. The grid workflow also creates legacy container/LED-index associations; it is not a dedicated physical-bin creation form. See design follow-up below. |
| Place bins into drawers | **PASS** | The ten existing unattached inventory bins were selected and drawn as two bin rectangles in each of five physical drawers; layout saved and bin/drawer attachment rows queried read-only. |
| Assign five controllers to cabinet | **PASS** | All five existing UI-created controllers were checked and saved in the graphical cabinet editor; saved slots 0–4 were read-only verified. |
| Reorder and restore controller order | **PASS** | Controller 1/2 order was swapped and saved through the browser, then restored and verified against the original five-controller order. |
| Configure physical strings and paths | **PASS** | One string per controller, lengths 200/250/150/300/100; each string had two completed ordered sections, forward then reverse. UI saved/reloaded wiring; ten path-section rows total 1000 pixels. |
| Save/reload five reference allocations | **PASS** | UI saved A–E ranges 25–34, 195–210, 225–234, 445–460 and 895–910. The editor computed expected controller-local splits. A second noncontiguous range 50–54 was also saved on Drawer 1. |
| Invalid, overlapping, over-capacity allocation | **PASS — rejection verified** | Start 0, overlapping 25–34, and 997+5 against 1000 pixels were rejected in the browser. Read-only snapshots confirmed allocation rows remained unchanged. |
| Stale allocation revision | **PASS — rejection verified** | Two browser pages loaded the same revision. One saved a valid Drawer 4 edit; the stale page’s Drawer 5 save returned the `led_error` path and did not alter Drawer 5 rows. The valid edit was restored through the UI. |
| Create ten parts and upload image | **PASS** | Ten parts with descriptions and part numbers were created via `/parts`; a generated PNG was uploaded for Part 1. |
| Assign inventory to bins / set starting quantities | **PASS** | Each part received a stock assignment through the browser visual bin picker; starting quantities were 8 and 5s. |
| Increase, decrease and move stock | **PASS** | HTMX +1 and -1 were exercised; stock was moved to another bin through the Move Stock form. Read-only queries verified conserved quantity and new assignment. |
| Mapping attestation | **PASS** | The admin checked the two Locate safety acknowledgements via each controller grid page. Each of the five loopback mocks received read-only `GET /json/info` and `GET /json/cfg`; all five attestations succeeded. |
| Locate noncontiguous ranges | **PASS (mock)** | UI Locate for bin 2 in Drawer 1 activated 1-based allocations 25–34 and 50–54 on Controller 1. Actual payloads targeted segment 10, indices `[24,34)` and `[49,54)`, color `[0,0,128]`. |
| Targeted Stop for noncontiguous ranges | **PASS (mock)** | Stop sent targeted black `[0,0,0]` commands for the same segment/ranges. No GlobalOff was issued. |
| Locate across controller boundary | **PASS (mock)** | UI Locate for bin 3 in Drawer 2 activated 195–200 on Controller 1 segment 20 with `[94,100)`, then 1–10 on Controller 2 segment 10 with `[0,10)`. |
| Targeted Stop across controllers | **PASS (mock)** | Stop blackened only Controller 1 segment 20 `[94,100)` and Controller 2 segment 10 `[0,10)`. No unrelated controller was written and no GlobalOff occurred. |
| Restart persistence | **PASS** | After all Locate sessions were stopped, the disposable server was terminated and restarted against the same DB and uploads. Browser reopened the cabinet; read-only snapshots of controller capacities, cabinet/drawers, bins/placements, wiring/paths, allocations, parts and assignments were identical before/after restart. |
| Camera/QR scanning | **NOT TESTED (device operation)** | The part-create pages report the duplicate scanner declaration described below. No camera permission or physical camera was used. |
| Application server logs | **NOT TESTED** | Harness child-process stdout/stderr is discarded. Browser HTTP responses and console/page errors were recorded; no server log claim is made. |

## Reproducible blockers

### Finding 1 — No browser workflow to edit controller settings

- **Status / severity:** **FAIL — P2**.
- **URL:** `/hardware`.
- **Reproduction:** create a controller through the visible “Add WLED Controller” form, return to `/hardware`, inspect its card and available actions.
- **Expected:** an edit action/form to correct name, IP address, port or controller settings.
- **Actual:** cards offer Configure (legacy grid), Export and Delete; there is no controller edit form/link. Source route inventory contains `POST /hardware` create and `POST /hardware/{id}/delete`, but no update route.
- **Relevant source:** `web/pages/hardware.templ:91-110,122-145`; `internal/router/router.go:220-228`; `internal/handler/hardware.go:45-78,122-137`.
- **State change:** none during inspection.
- **User impact:** correcting a saved address/name requires delete/recreate; deletion may become constrained after membership or inventory associations exist. Correct initial values can still complete the tested fresh-install happy path.
- **Recommended fix:** add a browser edit form and update route with normal auth/CSRF, validation and safe handling of controllers already assigned to cabinets or active Locate sessions.

### Finding 2 — Invalid TCP port accepted and persisted

- **Status / severity:** **FAIL — P2**.
- **URL/action:** submit the `/hardware` add-controller form with `ip_address=127.0.0.1` and `port=70000`.
- **Expected:** reject values outside TCP ports 1–65535 without changing controller state.
- **Actual:** `POST /hardware` redirected successfully and stored controller id 6 with port 70000. The isolated probe record was removed through its visible Delete form; no persistent test residue remains.
- **Relevant source:** `web/pages/hardware.templ:126-145` (number input has no min/max); `internal/handler/hardware.go:46-77` (parses integer/defaults zero but does not range-check).
- **State change:** temporary disposable row was created, then removed by browser UI; no data outside the temp DB was touched.
- **User impact:** a mistyped/out-of-range port can be saved and make the controller unusable; no edit UI exists to correct it in place.
- **Recommended fix:** validate `1 <= port <= 65535` server-side and expose the same min/max in the form; return actionable validation feedback without creating the row.

### Finding 3 — Duplicate QR scanner declaration on part-create pages

- **Status / severity:** **FAIL — P3**, consistent with Task 058.
- **URL/reproduction:** opening `/parts/new` once for each of the ten browser-created parts produced ten page errors: `Identifier 'html5QrcodeScanner' has already been declared`.
- **Expected/actual:** one scanner component/script per page; the shared layout and `PartForm` each include `BarcodeScannerModal`, causing duplicate `barcode_scanner.js` execution.
- **Relevant source:** `web/layouts/base.templ:27,109`; `web/components/part_form.templ:217-219`; `web/static/js/barcode_scanner.js:1`.
- **State change / impact:** none from the exception; all part forms, image upload, assignment and stock workflows completed. Camera QR scanning was not activated, so it is not claimed to be broken or working.
- **Recommended fix:** include the modal/script once and add a no-page-error browser assertion plus a camera-free callback test.

## UI design observation — physical bin creation is coupled to legacy grid workflow

The modern physical editor explicitly places an **existing unattached inventory bin** (`web/pages/storage_layout_editor.templ:54-61`); it does not create the inventory bin itself. In this run, ten bins were created through the advanced legacy controller grid and then placed physically through the cabinet editor. That browser route succeeded, but also gave each bin a legacy `container_id`/`led_index` as part of grid mapping. Those electrical fields remained separate from physical drawer geometry/attachments, and no legacy container was treated as a drawer.

This is not a failed UI-only creation, but it is an awkward coupling and should be reviewed against the required physical/electrical separation. A dedicated “create inventory bin” flow with optional independent legacy electrical mapping would avoid requiring a new installation to use the advanced segment grid merely to obtain stable bin identities.

## Evidence separation

### Browser-tested

- `web/tests/ui_only_install.browser.test.js` created all application records through the normal browser routes/forms. Its only database operations use a read-only SQLite URI and `PRAGMA query_only=ON`.
- The captured browser mutation list contains setup, controller forms/delete probe, cabinet/layout, controller membership, wiring, allocations, grid bin creation, parts, assignments, stock changes, mapping attestations, Locate and Stop. No SQL mutation exists in the test harness.
- Browser-generated reference translation and actual mock payload evidence are reported in the matrices above. Page errors consist of the known scanner declaration; the test recorded no other page errors or browser console errors.

### Integration-tested

- The requested full Go suite includes backup, stock, layout, CSRF, and mock Locate/Stop integration coverage; commands/results follow below. Those existing tests do not replace this no-seed browser run.
- Existing CSRF and layout browser tests also passed at this baseline; they use separate temporary environments and are not evidence for UI-only record creation because the layout harness seeds some fixtures through SQL.

### Source-reviewed

- Fresh install schema files are embedded through Goose migration 019.
- Controller UI is add-only for controller device records; current route list has no edit/update operation.
- Modern storage editor supports placement of existing bins; bin creation in this test used the legacy grid UI.
- New wiring lengths determine capacities; after save the controller rows contained exactly `[200,250,150,300,100]` pixels.
- The scanner modal/script is emitted in both the base layout and part form.

## Automated verification

| Command | Result |
|---|---|
| `CGO_ENABLED=1 go test -tags fts5 -count=1 ./...` | **PASS** |
| `CGO_ENABLED=1 go test -race -tags fts5 -count=1 ./...` | **PASS** |
| `CGO_ENABLED=1 go vet -tags fts5 ./...` | **PASS** |
| `CGO_ENABLED=1 go build -tags fts5 ./cmd/server ./cmd/mcp-server` | **PASS** |
| `npm test` | **PASS** — 20 grid-painter behavioral tests |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:csrf-browser` | **PASS** — existing CSRF browser regression; known duplicate scanner errors reported |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:layout-browser` | **PASS** — existing mixed-fixture layout/mapping browser test |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go node web/tests/ui_only_install.browser.test.js` | **PASS as an acceptance harness; product result PARTIAL** — the script completed and asserted all happy-path records/payloads/restart state, then recorded the two P2 findings and scanner errors |
| `node --check web/tests/ui_only_install.browser.test.js` | **PASS** |
| `git diff --check` | **PASS** — rerun after the report and handover edits |

## Remediation order

1. **P2 — Add controller update UI/route and server-side IP/port validation.** These are the only failed controller workflows in the tested installation path; regression tests should assert that invalid forms preserve DB state and controller corrections persist.
2. **P2 — Add an independent physical inventory-bin create workflow.** Keep bin identity/stock, physical placement, and legacy electrical mapping separate; do not reuse electrical containers as drawers.
3. **P3 — Remove duplicate QR scanner modal/script inclusion.** Verify no page error and test the scanner callback without accessing a camera.
4. **Follow-up acceptance —** add these cases to the UI-only browser suite, rerun the complete suite, and obtain independent source review before declaring the fresh-install experience complete.

## Readiness decision

The no-SQL happy path for the stated five-controller cabinet, drawers, bins, wiring, allocations, parts/stock, restart and mock Locate/Stop **was demonstrated**. The task's strict checklist nevertheless **does not pass** because controller edit is unavailable and invalid port validation is broken. No source fix was implemented. The full Task 059 acceptance answer is therefore **NO**, with the successful happy-path evidence distinguished from the two P2 failures.

No production/staging resource, historical inventory, deployment or physical WLED was used. Stop for independent review; do not begin Task 060.
