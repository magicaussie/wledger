# WLEDger OmO Development Handover

## Active session and authorization

This is the single active development handover document. `AGENTS.md` defines
development policy; the root `ROADMAP.md` records product requirements and design
updates. Historical handovers are references, not active instructions.

Current task: Task 045 M6 Physical Browsing, followed by M7 Stable Layout
Editing.

Status: M1–M5 and the M6/M7 implementation are published on
`development/chatgpt-m6-m7`. Recent Chromium acceptance found and fixed the
editor's JSON null-collection initialization failure that prevented Add Drawer
and Save Layout. The new disposable Playwright workflow now passes create, edit,
save/reload, bin association/detachment, Grid/List, View Bin Layout, validation,
archive and safe-delete checks. Continue treating full product acceptance as
pending owner review; M8 and later remain out of scope.

The current owner instruction authorizes M6/M7 source changes, isolated tests,
documentation, commits and pushes ONLY to `development/chatgpt-m6-m7`.
No merge to main, deployment, production database changes, destructive
migration, hardware operation or changes to the original archival checkout
are authorized. Preserve the additive physical-storage architecture: no
automatic legacy backfills, inventory remapping, or new Locate behavior.
Older authorization statements below are historical, not the current scope.

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
