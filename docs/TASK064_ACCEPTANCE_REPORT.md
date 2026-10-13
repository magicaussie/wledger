# Task 064 — Fresh Installation End-to-End Acceptance

- **Date:** 2026-10-14
- **Repository:** `magicaussie/wledger`
- **Starting commit:** `2fe170ffb9e14936db8edbff921e08279e2a6983`
- **Ending application-source commit tested:** `2fe170ffb9e14936db8edbff921e08279e2a6983` (no application source or migration changes were made)
- **Overall result:** **PARTIAL**
- **Critical Locate/Stop safety invariant:** **No failure observed in the exercised routes or automated safety suite.** This is not a pass for the unexercised active-session failure/recovery browser scenarios.
- **Publication:** Owner-approved baseline formatting exception applied: repository-wide `gofmt` remains FAIL only for the 42 unchanged starting-commit files listed below. Task 064 changed-file formatting and all other verification gates passed. The scoped acceptance artifacts are published on `development/chatgpt-m6-m7`; protected pre-existing changes were not included.

## 1. Scope and isolation

The acceptance ran on branch `development/chatgpt-m6-m7` at the requested starting commit. A fresh Task 064 browser harness, `web/tests/task064_fresh_install.browser.test.js`, builds the server into a newly created OS temporary directory, starts it on an available loopback port, and uses the newly created `data/wledger.db`. Its cleanup removes that directory after each run. It does not open or reuse Task 059's database.

The browser uses Chromium. All five WLED endpoints are independent mock HTTP servers bound to `127.0.0.1` on ephemeral ports. No physical controller was contacted. The no-controller workflow was also checked separately by `web/tests/physical_first.browser.test.js`; its mock rejects any unexpected WLED request and recorded `wledRequests: []`.

Application records are created and changed through normal browser routes/forms. Direct SQLite access in the Task 064 harness uses `mode=ro` and `PRAGMA query_only=ON` for counts and assertions only. The browser runs do not access `.task055b-staging/`, existing service ports, production resources or existing staging databases. Temporary databases/uploads are discarded by test teardown.

The initial working tree contained pre-existing modifications to `docs/OMO_HANDOVER.md` and untracked `.task055b-staging/` and `docs/TASK056_FUNCTIONAL_AUDIT.md`. These files were not read for test data, modified, staged or removed.

## 2. Fresh-install browser results

`npm run test:fresh-install` completed successfully as a test harness and emitted a JSON evidence record. Its application-workflow result is marked PARTIAL because it does not automate every active-Locate failure/recovery and cross-install browser restore scenario in the task checklist.

| Workflow | Result | Evidence |
|---|---|---|
| Fresh startup and migration | **PASS** | Blank temporary DB; migration IDs 1 through 19 were read-only verified in `goose_db_version` as applied. Initial controllers, cabinets, drawers, bins, containers, cabinet memberships, bin placements, parts, stock assignments, strings, paths and allocations all counted zero. |
| Setup, dashboard, login | **PASS** | Setup form native validity rejected malformed email/short password; valid administrator setup reached the dashboard. A wrong-password login returned the generic “Invalid email or password”; valid login succeeded. Invalid setup was checked at the browser form-validation boundary, not submitted with client validation bypassed. |
| Physical-first workflow without WLED | **PASS** | Separate fresh browser instance created a cabinet, drawers, a physical bin, part and stock using browser UI with no controllers/containers, no pixel mapping and no WLED requests. |
| Cabinet and drawer editor | **PASS** | Main fresh harness created a 200×120 cabinet and five drawers through the visual editor. The existing layout browser regression also exercised editor save/reload and lifecycle operations. |
| Physical bin creation | **PASS** | Main fresh harness created ten bins, two per drawer, using `/storage/cabinets/{id}/drawers/{drawerID}/bins` via the physical-bin UI. Read-only assertions verified stable IDs, drawer attachments, and NULL legacy `container_id`/`led_index`; bin creation did not change electrical containers. It did not use the legacy electrical grid. |
| QR and identity behavior | **PASS / PARTIAL** | Main harness requested PNG QR images for all ten stable bin IDs. Separate physical-first browser test verified QR digest stability across rename, reposition, cross-drawer move and detach while stock stayed unchanged. A replacement-bin lifecycle was not separately exercised. |
| Inventory workflow | **PASS** | Ten parts were created through browser forms, assigned to bins and stocked. Increase/decrease, stock move with quantity conservation, search filtering, inventory location display, and image upload were exercised. |
| Controller management | **PASS** | Five UI-created loopback mock controllers used custom ephemeral ports. Controller edit preserved its ID and endpoint. Port `70000` was rejected both by form validity and a browser-originated request bypassing client validation (HTTP 400, no row created). Duplicate endpoint submission was rejected (HTTP 400, no row created). Cabinet association/order and later capacity values were verified. |
| Physical wiring and mapping | **PASS** | Five controller memberships; configured capacities `[200,250,150,300,100]` (1000 total); one physical string per controller; two ordered path sections per string including reversed/serpentine directions; multiple ranges and saved allocations. Start-at-zero, overlap, over-capacity and stale-revision cases were rejected without changing saved allocations. |
| Locate/Stop normal paths | **PASS — mock only** | Fresh harness attested all five mock mappings and exercised five drawer locations spanning controllers 1–5, including ranges crossing controller boundaries and noncontiguous ranges. Nine activation writes and nine Stop writes were captured. Assertions required exact segment and pixel intervals, exact targeted black cleanup, no unrelated controller/pixels, and no `GlobalOff`. |
| Administrator/editor/viewer authorization | **PASS — exercised subset** | Admin created editor/viewer accounts through Settings. Both completed forced password reset. Editor created inventory and received HTTP 403 for controller creation; viewer received HTTP 403 for inventory create routes/direct mutation and had no controller action. Broader per-route permission matrix was not exhaustively tested. |
| CSRF | **PASS** | Fresh Task 064 browser mutations used normal forms; existing CSRF browser test passed and verified token rotation, form/HTMX tokens, forged mutation rejection and restore without CSRF rejection. |
| Browser/runtime/mobile | **PASS — exercised subset** | No page JavaScript exceptions; scanner regression reports one modal/script and no duplicate declaration. Search and cabinet reload worked. Narrow-width check verified the mobile navigation control at 390 px; this was not a complete visual/mobile layout audit. |
| Idle restart persistence | **PASS** | App restarted against the same temporary database after Locate sessions had been stopped. Read-only before/after snapshots of controllers, cabinet, drawers, bins, placements, wiring, paths, allocations, parts, images and stock assignments matched exactly. Session remained authenticated. |

### Mock request evidence

The fresh-install JSON record showed only loopback mock-controller traffic. Mapping attestation made read-only `GET /json/info` and `GET /json/cfg` requests. Locate and Stop used `GET /json/state` for verification and targeted `POST /json/state` commands. Every captured command matched the expected controller, WLED segment and half-open pixel interval. Stop used only black color commands for the owned ranges; no payload contained controller GlobalOff/power-off state.

No screenshots were retained. The browser test's structured JSON output, assertions and request/payload capture are the recorded evidence.

## 3. Backup, security and safety integration evidence

These checks are integration/unit evidence, not a complete fresh-install browser restore journey:

- **Backup/export/restore:** the existing CSRF browser test passed archive download, nonempty archive check, rejection of restore without a CSRF token without data mutation, and a successful UI restore in its disposable database. Go backup tests passed export/restore round-trips, physical layout/controller/wiring/allocation preservation, malformed/envelope rejection, archive resource limits, rollback on injected write failure, and refusal to restore when durable Locate ownership exists.
- **Cross-fresh browser restore:** not exercised through the Task 064 browser harness into a separately set-up fresh app. The separate database service-level restore tests are not a substitute for that browser flow.
- **API bearer tokens:** the full Go suite passed, including malformed, duplicate/ambiguous, wrong-scheme, empty and valid token cases in `internal/api`.
- **CSRF and role gates:** existing CSRF browser tests and the Task 064 role checks passed. The new browser run tested representative editor/viewer capabilities, not every mutation endpoint.
- **Supplier image proxy / upload security:** application Go tests passed, but unsafe-destination behavior and all upload-size boundaries were not exercised in the Task 064 browser session.
- **Locate ownership and failure safety:** the full Go suite passed. `internal/wled` tests include concurrent ownership, cross-connection configuration blocking, partial activation cleanup, timeouts, bounded retries, restart recovery, cleanup commit failure, cancellation, stale/replacement session handling, and exact targeted command tests. This is automated integration evidence. Task 064 did not simulate an active Locate crash, simultaneous browser Locate conflict, or mock timeout/partial failure end-to-end.

No critical safety violation was observed. Because active-session browser failure/recovery cases remain untested, the Locate/Stop subsystem and overall task remain **PARTIAL**, not PASS. Physical hardware behavior is **NOT TESTED**.

## 4. Verification gates

| Gate | Result | Notes |
|---|---|---|
| Repository-wide `gofmt` | **FAIL — pre-existing formatting findings** | Compared a clean archive of starting commit `2fe170ffb9e14936db8edbff921e08279e2a6983` with the Task 064 working tree using Go 1.25.5. Sorted `gofmt -l` filename lists were identical (42 files); no formatting finding was introduced by Task 064. |
| Task 064 changed-file formatting | **PASS** | Task 064 adds no Go files; the changed JavaScript file passes `node --check`. No unrelated Go files were reformatted. |
| `templ generate` | **PASS** | `updates=0`. |
| `CGO_ENABLED=1 go test -count=1 -tags fts5 ./...` | **PASS** | Full suite passed using host Go 1.25.5 at `/home/spetchal/gotoolchain/go/bin/go`. |
| `CGO_ENABLED=1 go test -race -count=1 -tags fts5 ./...` | **PASS** | Full race suite passed. |
| `CGO_ENABLED=1 go vet -tags fts5 ./...` | **PASS** | No diagnostics. |
| `CGO_ENABLED=1 go build -tags fts5 ./cmd/server ./cmd/mcp-server` | **PASS** | Both packages built. |
| `npm test` | **PASS** | 20 grid-painter behavioral tests. |
| `npm run test:csrf-browser` | **PASS** | Existing CSRF browser suite. |
| `npm run test:layout-browser` | **PASS** | Layout editor, physical-first, and barcode scanner browser regressions all passed. |
| `npm run test:fresh-install` | **PASS as harness; acceptance PARTIAL** | New Task 064 fresh-install workflow passed its assertions; test output explicitly marks coverage PARTIAL. |
| `node --check web/tests/task064_fresh_install.browser.test.js` | **PASS** | JavaScript syntax check. |
| `git diff --check` | **PASS** | No whitespace errors in working-tree changes. |
| `git diff --cached --check` | **PASS** | Scoped staged diff has no whitespace errors. |

All reported verification gates other than repository-wide gofmt passed. Docker was not needed because the host Go 1.25.5 toolchain was available.

### Exact gofmt baseline exception

The same 42 paths were reported by `gofmt -l` at the unchanged starting commit and in the Task 064 worktree. A clean `git archive` of the starting commit was checked with the same Go 1.25.5 `gofmt`; a sorted filename-list `diff -u` returned no differences. None of these files was modified or reformatted:

- `internal/db/physical_locate_sessions.sql.go`
- `internal/handler/walls.go`
- `internal/i18n/i18n.go`
- `internal/i18n/i18n_test.go`
- `internal/importer/parts.go`
- `internal/qrcode/qr.go`
- `internal/suppliers/dto.go`
- `internal/suppliers/providers/altronics/provider.go`
- `internal/suppliers/providers/amazon/provider.go`
- `internal/suppliers/providers/amazon/provider_test.go`
- `internal/suppliers/providers/arrow/provider.go`
- `internal/suppliers/providers/autobarn/provider.go`
- `internal/suppliers/providers/autobarn/provider_test.go`
- `internal/suppliers/providers/avnet/provider.go`
- `internal/suppliers/providers/bunnings/provider.go`
- `internal/suppliers/providers/component-distributor/provider.go`
- `internal/suppliers/providers/datasheet/provider.go`
- `internal/suppliers/providers/digikey/provider.go`
- `internal/suppliers/providers/element14/provider.go`
- `internal/suppliers/providers/farnell/provider.go`
- `internal/suppliers/providers/future-electronics/provider.go`
- `internal/suppliers/providers/heilind/provider.go`
- `internal/suppliers/providers/jaycar/api.go`
- `internal/suppliers/providers/jaycar/bff.go`
- `internal/suppliers/providers/jaycar/fetcher.go`
- `internal/suppliers/providers/jaycar/listing.go`
- `internal/suppliers/providers/jaycar/parser.go`
- `internal/suppliers/providers/jaycar/parser_test.go`
- `internal/suppliers/providers/jaycar/provider_test.go`
- `internal/suppliers/providers/lcsc/provider.go`
- `internal/suppliers/providers/officeworks/provider.go`
- `internal/suppliers/providers/providers.go`
- `internal/suppliers/providers/rs-components/provider.go`
- `internal/suppliers/providers/rs-components/provider_test.go`
- `internal/suppliers/providers/rutronik/provider.go`
- `internal/suppliers/providers/semikron/provider.go`
- `internal/suppliers/providers/spotlight/provider.go`
- `internal/suppliers/providers/spotlight/spotlight_manual_test.go`
- `internal/suppliers/providers/supercheap/provider.go`
- `internal/suppliers/providers/tme/provider.go`
- `internal/suppliers/service_import_test.go`
- `internal/suppliers/service_resync_test.go`


## 5. Defects and changes

No application defect preventing the tested acceptance path was found at starting commit `2fe170ffb9e14936db8edbff921e08279e2a6983`. The tested controller edit/port validation, independent physical-bin flow, and single scanner include were already present at this baseline. No application source, schema, migration or production configuration was changed.

Task 064 adds only:

- `web/tests/task064_fresh_install.browser.test.js` — fresh isolated browser acceptance and evidence capture.
- `package.json` — `test:fresh-install` script entry.
- `docs/TASK064_ACCEPTANCE_REPORT.md` — this report.

## 6. Remaining limitations and disposition

Overall **PARTIAL**. Remaining explicitly untested acceptance areas include:

- Active Locate crash and timeout recovery in the browser against mock controllers.
- Conflicting Locate requests under failure conditions in the browser.
- Backup/restore through the browser into a separately initialized fresh installation (and the complete UI malformed/oversized archive/rollback matrix).
- Physical hardware operation; no physical WLED controller was contacted.

Full mobile visual coverage and exhaustive per-route authorization/proxy/upload boundary coverage also remain follow-up work. Normal Locate/Stop payload safety passed across all five mock controllers, and the automated durable-ownership safety tests passed. No untested item above is presented as passed.

The owner-approved baseline exception permits publication without reformatting unrelated Go files. Only the three Task 064 artifacts were published. No application source or migrations were changed. No deploy, production/staging change, physical hardware request or production-readiness claim was made.
