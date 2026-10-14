# WLEDger v1.0 — Release Completion Audit and Fixed Finish Plan

**Audit date:** 2026-10-14
**Repository:** `magicaussie/wledger`
**Branch / baseline:** `development/chatgpt-m6-m7` at `526997fa11d828c92800c99e76d71bc6433bfb99`
**Remote check:** `origin/development/chatgpt-m6-m7` resolved to the same SHA at audit start.
**Audit outcome:** Core inventory use is demonstrated in an isolated installation, but WLEDger is **not accepted for v1.0 release**. No application code was changed during this audit.

## 1. Executive summary

WLEDger is a real, usable inventory application on the audited development baseline. A fresh database was initialized through the actual server; an administrator was created and authenticated in Chromium; cabinets, drawers, physical bins, controller/wiring configuration, parts, bin assignments and stock were created through the UI. Stock increments, decrements and transfers conserved quantities. Data survived an application restart. A browser-driven backup was restored into a separately initialized disposable application/database, with inventory, layout, wiring, images and settings compared. Normal Locate/Stop was exercised only against loopback mocks.

The latest browser run is strong positive evidence, not a release certificate. Its harness exits successfully but deliberately reports overall `PARTIAL`: the exercised UI workflow does not cover all requested lifecycle actions, and its conventional WLED path is mock evidence only. The full Go suite, race suite, vet, builds, JavaScript tests and the focused browser suites passed in this audit.

**No critical inventory-data corruption, normal stock-quantity defect, or current CSRF bypass was reproduced or source-confirmed in the audited paths.** Earlier findings were reassessed:

- Task 056's CSRF gap was remediated by Task 057. Current router middleware and the browser CSRF regression confirm the tested protections.
- The duplicate scanner declaration reported in Tasks 058/059 is fixed on this baseline; the scanner regression passes with one modal/script and no page errors.
- Task 059's controller-edit and invalid-port failures are fixed; the fresh-install browser run edited a controller and proved port `70000` was rejected without a row change.
- Task 067B's delayed-command failure is a real failure of its deliberately controlled mock delivery model. It does **not** prove stock WLED applies a fully received, already-parsed command after a later Stop. Task 067C/067D source review nevertheless leaves a credible, unresolved risk: stock WLED exposes no cross-connection generation fence or command-drain receipt, and the actual intended firmware/controller was not tested.

The smallest credible v1.0 boundary is **Profile A — inventory-only**. The default-off server-side WLED output guard was completed in commit `c3fa25d`; physical cabinet/drawer identities and QR/Code 128 scan-driven stock transfer were completed in `bbec5a5`. The remaining bounded acceptance work is part image removal plus full image/stock lifecycle browser verification and a final release-candidate gate. Do not reopen those completed guard/scanning workstreams or require custom WLED firmware work for inventory-only v1.0.

## 2. Baseline, preservation and isolation

- `HEAD` and the requested starting SHA both equal `526997fa11d828c92800c99e76d71bc6433bfb99`.
- Current branch: `development/chatgpt-m6-m7`.
- `git ls-remote origin refs/heads/development/chatgpt-m6-m7` returned the same SHA; no remote divergence was observed.
- At start, the worktree contained modified `docs/OMO_HANDOVER.md` and untracked `.task055b-staging/` and `docs/TASK056_FUNCTIONAL_AUDIT.md`. All three were preserved. The staging tree was not opened, used, altered or staged. The handover and Task 056 report were read as requested but not edited.
- Browser harnesses built and started the application in fresh OS temporary directories with disposable SQLite databases and uploads. Controllers were loopback HTTP mocks bound to `127.0.0.1` on ephemeral ports. The restore test used a second fresh app process, database and uploads directory.
- No request was sent to production port 8090 or the existing staging port 8091. No existing production/staging database, service, user data or physical WLED controller was accessed.
- Test-created processes, databases, uploads and mock endpoints were torn down by the harnesses. No global dependency was installed.

## 3. Evidence and classification rules

- **PASS** — the stated scope was exercised end-to-end in the browser, or (where explicitly labeled) by a relevant integration test.
- **FAIL** — a reproducible current defect or missing requested operation has direct evidence.
- **PARTIAL** — part of the workflow passed, but an important requested result or acceptance boundary was not demonstrated.
- **NOT TESTED** — there is no adequate evidence. This is not a claim that the feature is broken.

Browser evidence is kept separate from unit/integration tests and source review. In particular, loopback WLED payloads are not physical-controller evidence.

## 4. What works today

| Workflow | Audit result | What the evidence demonstrates / boundary |
|---|---|---|
| Fresh install and database initialization | **PASS — browser** | Current disposable app started from an empty DB; all migration versions 1–20 were applied; expected application tables began empty. First-run setup created the initial administrator. |
| Login/authentication | **PASS — browser, selected cases** | Setup, logout, invalid-password feedback, valid login and session use passed. Editor/viewer first-login password reset and role checks also passed in the fresh-install journey. This is not exhaustive session-expiry/fixation coverage. |
| CSRF and role boundaries | **PASS — tested scope** | `npm run test:csrf-browser` passed; current `internal/router/router.go` installs CSRF middleware globally, and route-level regressions cover mutation routes. Browser scenarios reject tokenless writes. Browser role checks deny tested viewer/editor actions. Not every role/route/security combination was dynamically exercised. |
| Cabinet/drawer editing | **PASS — browser, tested scope** | UI-created cabinet, dimensions and five drawers persisted; layout editor save/reload and selected movement/resize/validation/lifecycle cases passed. Archive/delete protections were exercised in layout browser tests. Drawer renumber/reorder was not separately verified in the fresh-install run. |
| Physical bins | **PASS — browser** | Ten physical bins were created in the drawer UI, attached to drawers, and verified with no legacy `container_id`/`led_index`; inventory bins remained independent of electrical containers. Physical bin placement and selected detach/move/lifecycle cases passed in the layout suite. |
| Parts and assignments | **PASS — browser, selected cases** | Parts were created, edited and assigned to UI-created bins. Part create/edit and image upload also passed in the CSRF browser test. Part deletion and every validation/failure rollback path were not browser-tested. |
| Stock quantities | **PASS — browser, tested operations** | UI assignment, `+1`, `-1`, and move-to-another-bin were exercised. The move preserved quantity; assignments and quantities matched after restart. Removing a stock assignment was not browser-tested. |
| Search | **PASS — basic query only** | Search for a named part filtered the browser results correctly. Filter combinations, sorting and pagination were not tested. |
| Images | **PARTIAL** | Initial image upload persisted; backup contained image and thumbnail; restore placed the asset in the independent destination and its database reference survived restart. Image replacement is service-tested but not browser-tested. The current part form has no remove-image control, so removal is not a supported UI workflow (see finding B-2). Image MIME/path/XSS adversarial testing was not part of this audit. |
| Controller entities and wiring | **PASS — browser, mock endpoints** | Create/edit controller, reject port 70000 and duplicate endpoint, assign/reorder five controllers, configure five strings and ordered path sections, save allocations and validate capacity/overlap/stale revisions. These requests used loopback-only endpoints. |
| Mapping preview and Locate/Stop | **PASS — mock only** | Fresh-install browser configured and attested five loopback mocks; five representative locations exercised noncontiguous ranges and controller-boundary translation. Stop sent targeted black ranges. Task 065's stateful framebuffer checks verify owned ranges are cleared and unrelated pixels preserved for the exercised failure/recovery cases. This proves neither physical firmware ordering nor real light output. |
| Backup and restore | **PASS — browser, separate disposable apps** | Authenticated UI export from one app was restored into a separately initialized destination. Stable inventory/layout/wiring/allocation/settings/image records and ten bin QR PNGs were compared; the destination remained browseable after restart. Restore and startup recovery generated zero WLED writes. Task 066 Go subprocess tests additionally cover restore journal rollback/roll-forward after process death. Hostile-archive browser limits are not exhaustive. |
| Restart/persistence | **PASS — browser** | UI-created controllers, cabinet/drawers, bins/attachments, wiring, allocations, parts, image references and stock snapshot matched before and after app restart. Restored destination also remained browseable after restart. |
| QR/barcode | **PARTIAL — image/routes work; physical hierarchy acceptance incomplete** | Browser runs returned QR PNGs for stable bin identities and restore preserved QR bytes. `internal/handler/scan_test.go` covers selected bin/controller/container redirects. The browser harness did not decode generated PNGs and follow camera-free scans through all destinations. Existing `cabinet` and `drawer` scan codes point at legacy controller/container routes, not the new physical cabinet/drawer pages; there are no physical-storage cabinet/drawer QR routes. |
| Scanner UI | **PASS — mock scanner only** | `npm run test:layout-browser` includes a camera-free scanner stub; one scanner instance/modal/script, open/close/reopen and no duplicate declaration/page errors passed. No camera permission, actual camera or physical scanner was used. |
| Responsive/general usability | **PARTIAL** | Layout editor tested at narrow width in prior browser acceptance; current fresh-install check confirms mobile navigation at 390px. No whole-application mobile visual, accessibility, keyboard/focus, or every-dialog review was performed. |

## 5. Failed workflows and demonstrated defects

### Current normal-use failures

There was **no reproduced failure** in fresh setup, part creation/editing, stock assignment/increment/decrement/transfer, basic search, physical bin creation, restart persistence, backup/restore, or conventional normal Locate/Stop against the specified loopback mocks.

Two requested v1 workflows are currently missing or incomplete:

1. **Physical cabinet/drawer QR navigation — FAIL against the requested physical-storage semantics.** Current `CabinetScanCode` represents a controller and `/scan` redirects it to `/hardware/{id}/grid`; `DrawerScanCode` represents a legacy container and redirects it to `/drawers/{id}`. The current routes are registered at `/cabinet/{id}/qr` and `/drawer/{id}/qr`; no equivalent QR destination for the new physical `cabinets` and `drawers` model was found. Those legacy routes may remain valid for legacy labels, but they do not demonstrate the requested physical cabinet/drawer scan journey.
2. **Remove a part image — FAIL as a supported UI operation.** `web/components/part_form.templ` displays the current image and an upload input labeled for replacement, but supplies no remove action/field. `internal/parts.Service.UpdatePart` retains the old image when no replacement is uploaded. This is not an image corruption finding; it is a missing requested operation. Image replacement itself has service tests but was not browser-verified.

Neither finding presently prevents ordinary part/quantity operations. Both prevent claiming the requested complete v1 acceptance without either a narrowly scoped fix or an explicit owner-approved scope change.

### Earlier findings reassessed against the current baseline

| Earlier finding | Current status and evidence |
|---|---|
| Task 056 missing CSRF enforcement | **Fixed.** Global `RequireCSRF` is registered in `internal/router/router.go`; current CSRF browser suite passed. Do not retain this as a current blocker. |
| Task 058/059 duplicate QR scanner script | **Fixed.** The current barcode scanner browser regression passed: one modal/script, no duplicate-declaration error. |
| Task 059 missing controller edit and invalid port accepted | **Fixed.** Current Task 064 fresh-install browser run edited a saved controller and received HTTP 400 for port 70000 with no controller row added. |
| Task 065 interrupted-restore concern | **Subsequently addressed in Task 066 implementation.** Current full Go suite and separate-install browser restore/startup rollback/roll-forward checks passed. This audit did not repeat every lower-level hard-kill boundary through the browser. |

## 6. Untested workflows (not reported as broken)

- Browser-driven image replacement/removal; the source offers replacement, but the UI has no removal action.
- Browser-driven stock-assignment removal, part deletion, duplicate/invalid part submissions, and proof of unchanged DB/uploads for each failed form.
- Drawer ordering/renumbering acceptance and all delete/archive permutations with stock assigned. Selected lifecycle checks passed; do not infer every permutation.
- Search filters, sort and pagination; search was tested for a single query.
- QR PNG decoding and actual end-to-end navigation from scanned part/bin codes in a browser. Camera-free scanner initialization passed, but camera/scanner callback-to-destination flow was not run for all entity types.
- Physical cabinet/drawer QR navigation is absent as described above, rather than merely untested.
- Broad mobile responsiveness, accessibility, focus/error announcements, keyboard-only form use, and all navigation/dialogs.
- Adversarial uploads (content/MIME/path/size), stored-XSS probes and comprehensive secret redaction from logs/exports.
- Exhaustive role matrix, session expiration and password-change lifecycle; selected CSRF and role scenarios passed.
- Browser-driven malformed/oversized backup archive matrix. Go backup tests cover many archive/rollback limits; no claim of a complete adversarial browser matrix.
- WLED firmware or physical controller behavior. No exact intended controller model, firmware, bus driver, network environment or physical installation was identified/tested.

## 7. Defects versus exceptional risks

### A — Release blockers

**A-1 — Missing inventory-only WLED write fence (release-profile blocker; not a demonstrated stock workflow failure).** The current router always registers physical and legacy WLED write routes, including `POST /parts/{id}/locate-bin`, `/storage/drawers/{id}/locate`, `/hardware/{id}/locate`, `/drawers/{id}/locate`, and `/hardware/off` (`internal/router/router.go`). No inventory-only runtime/build profile was found. A fresh installation normally requires explicit controller mapping attestation before modern physical Locate, but that is not equivalent to guaranteeing that an inventory-only release cannot send WLED writes: the routes and legacy/global-off paths remain present. Before Profile A is accepted, block WLED state-changing operations server-side by default and test the negative behavior; hiding buttons alone is insufficient. Preserve only any deliberately specified, targeted cleanup needed for an already persisted unresolved session.

This is a **release-scope safety gap**, not evidence that ordinary inventory use is unsafe or that an operator accidentally activated LEDs during this audit.

**Count:** zero demonstrated critical inventory-data corruption/security defects; one required release-profile guard is outstanding.

### B — Important v1 gaps

- **B-1 Physical QR identity/routing mismatch:** current scanner prefixes model legacy controller/container records. The required new physical cabinet/drawer navigation is not available in that QR flow. Concrete evidence: `internal/handler/parts.go` (`CabinetScanCode`, `DrawerScanCode`, `HandleScan`), `internal/handler/hardware.go` (QR handlers), `internal/router/router.go` (routes), and `web/pages/bin_labels.templ` (labels generated for controllers/containers).
- **B-2 Part image removal absent:** no remove control or remove request in `web/components/part_form.templ`; an edit without a new upload preserves the existing image in `internal/parts/service.go`. If removal is retained in the v1 requirements, add an explicit safe remove operation and browser test. Do not silently erase an existing image during an unrelated failed update.
- **B-3 Browser acceptance gaps:** stock-assignment removal, part deletion, image replace/remove and relevant failed-form non-mutation are not all demonstrated end-to-end. These are acceptance gaps unless a reproducible run identifies an additional defect.

### C — Exceptional WLED risks (defer physical Locate)

**C-1 Delayed in-flight activation / ordinary stock WLED semantics.** Task 067B deliberately held an already received mock request before applying it, killed the sending process, allowed a recovery Stop to be acknowledged and durable ownership to be released, then applied the older activation. The mock framebuffer relit afterwards. This is a **demonstrated failure under that mock delivery model**. It does not establish that the intended physical WLED firmware queues an already fully parsed request in that way. Task 067C's source review found synchronous logical-state deserialization for complete JSON writes and did not find a post-parse command queue matching the mock pause. However, asynchronous network handlers and independent connections expose no documented cross-connection FIFO, generation rejection, or drain barrier; HTTP success is not a correlated bus-completion receipt. The exact controller/firmware/bus behavior remains unknown. Treat this as a real unresolved risk, not either proven physical relight or a dismissed synthetic case.

**C-2 Other-writer/process boundary.** SQLite `BEGIN IMMEDIATE` coordination passed selected two-process conflict/recovery tests, but external WLED clients are outside that coordination. Supported local-filesystem SQLite locking and a single designated WLEDger writer remain operational assumptions for any future WLED-enabled deployment.

**C-3 Physical validation unavailable.** No controller identity, firmware, output/bus map, external-writer list, network ACL or real LED behavior was validated. No physical test is authorized by this audit.

### D — Post-v1 enhancements / separate milestones

- Fenced Locate requiring custom WLED core integration, persistent controller generation/epoch, apply-point checks and a correlated command/output-drain receipt (Task 067C/067D). A standalone usermod or sender-side delay is not enough. This is a separate, bounded product milestone only after exact target hardware and firmware are selected.
- Expanded accessibility and whole-site responsive testing.
- Full browser attack/abuse matrix for every upload, OAuth, supplier and archive edge.
- Multi-host/horizontal scaling or non-SQLite distributed hardware ownership.
- Any physical Locate commissioning, deployment, production data migration or hardware action (separate explicit authorization required).

## 8. v1.0 release profile decision

| Profile | Audit assessment | Decision |
|---|---|---|
| **A — Inventory-only; physical Locate disabled** | Smallest viable boundary for cabinets, drawers, bins, parts, stock, search, images, QR and backup/restore. Core inventory browser workflows passed. It still needs the server-side WLED write fence, physical cabinet/drawer QR semantics, image-removal decision/fix, and the short remaining lifecycle acceptance. | **Recommend.** Do not claim ready until backlog items 1–3 and the release gate pass. |
| **B — Inventory plus conventional WLED Locate/Stop** | Normal commands and targeted Stop passed against stateful loopback mocks, including selected failure/recovery cases. Stock WLED has no demonstrated stale-command fence/drain receipt; Task 067B's failure model is not proof of physical behavior, but Task 067C/D leaves the risk unresolved. No actual intended firmware was tested. | **Do not include in v1.0.** Reconsider only after explicit risk acceptance, controlled single-writer/network policy and separately authorized exact-device acceptance. This audit did not enable physical operation. |
| **C — Fully fenced Locate** | Requires a custom WLED core/protocol path; the Task 067D host model is not a firmware implementation/build/device test. | **Defer.** Separate future milestone; not necessary for usable inventory v1.0. |

For Profile A, retain controller configuration only if it cannot cause output changes. Enforce the no-write boundary in server handlers/services for physical Locate, legacy Locate/flash and GlobalOff, not just navigation or form visibility. Avoid migrations or broader architecture work for this guard.

## 9. Fixed prioritized completion backlog

Effort is focused engineering time, not calendar elapsed time. Ranges include implementation plus its stated acceptance test; estimate uncertainty is medium because form/UI correction scope is not yet exercised.

| Priority | Specific remaining work and evidence | Required change | Acceptance test | Effort | Dependencies | Required for inventory-only v1.0? |
|---|---|---|---|---:|---|---|
| **P0 — COMPLETE (`c3fa25d`)** | **Default-off server-side WLED output guard.** | No change in this inventory lifecycle task. Keep default output disabled; physical activation remains excluded from v1.0 inventory acceptance. | Completed guard implementation and regression verification are recorded in the existing checkpoint. Do not repeat or reopen this workstream here. | Complete | Published before this task. | **YES — completed release gate.** |
| **P1 — COMPLETE (Tasks 2–3; `bbec5a5`)** | **Dual-format identity labels and scan-driven stock transfer.** | No change in this inventory lifecycle task. Retain stable QR/Code 128 identities, physical cabinet/drawer routing, legacy electrical identity separation, and explicit transfer confirmation. | **PASS:** the existing fresh disposable browser acceptance decoded both formats for cabinet, drawer, bin and product; confirmed camera/keyboard scanning, manual selection, cancellation/confirmation, invalid inputs and legacy separation; WLED output remained disabled with zero requests. Rerun the regression suite only; do not repeat implementation. | Complete | Published before this task. | **YES — completed release gate.** |
| **P1 — COMPLETE (inventory lifecycle checkpoint)** | **Complete inventory image and stock lifecycle acceptance.** | Added explicit image removal; verified upload/replacement/no-change/removal, create/edit, physical-bin assignment, quantity adjustment, manual and scan-driven movement, assignment removal, product deletion, search, failure/cancel non-mutation and restart persistence. | **PASS:** fresh disposable browser app with `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false`; a separate disposable app/database restored image assets, inventory and stock. Full Go/race/vet/build/JS and applicable browser checks passed. No WLED state writes occurred. | Complete | The default-off WLED guard and dual-format scan workflow remain separate completed checkpoints. | **YES — completed release gate.** |
| **P1 — final gate (no new feature task)** | Run one release-candidate pass after the above changes; current reusable suites already cover fresh install, separate restore, CSRF and layout. | Freeze v1 inventory-only scope and document the default WLED-disabled deployment setting/guarantee, backup procedure and exact supported QR identities. No new roadmap or numbered workstream. | Complete the Definition of Done below from a clean disposable install; run Go tests/race/vet/build, JS and browser regressions; inspect exact changed-source diff; verify only loopback mocks were contacted and no production/staging paths were used. | **0.5–1.0 day** | Backlog items 1–3. | **YES — release sign-off.** |

**Shortest practical route:** complete the P0 write fence, then the two bounded P1 UI items, then one release-candidate run. Estimated total: **3–6 focused working days** (0.5–1 + 1–2 + 1–2 + 0.5–1). This estimate is grounded in the existing passing browser harnesses and the direct source gaps above; it is not a promise. If image removal or physical QR behavior causes an unanticipated data-model change, stop and revise the estimate rather than expand scope silently.

## 10. Release definition of done — Profile A

Status below distinguishes observed evidence from proposed completion acceptance.

| Gate | Current evidence | Release status |
|---|---|---|
| 1. Fresh install succeeds | **VERIFIED**: current disposable browser app started from empty DB; migrations 1–20 applied; setup completed. | Pass. |
| 2. Core inventory workflows pass in real browser | **PASS — browser:** create/edit, physical bin assignment, stock adjustment, manual and scan-driven transfer, assignment removal, product deletion, duplicate/invalid handling, cancellation, search and restart persistence. | Pass. |
| 3. Data survives application restart | **VERIFIED**: browser-created installation snapshot matched before/after restart. | Pass. |
| 4. Stock operations preserve quantities | **PASS — browser:** increase/decrease, manual and scan-driven movement, removal of only the selected assignment, product-delete cascade and restart snapshot. | Pass. |
| 5. Images work | **PASS — browser and restore:** initial upload, replacement, edit without image change, explicit removal, failed-update preservation, file cleanup and independent backup/restore of the retained image/thumbnail. | Pass. |
| 6. Stable QR navigation and legacy separation | **VERIFIED** in the Task 2 follow-up using disposable browser and decoded physical plus legacy QR identities. | Pass for the tested QR scope; does not prove Code 128 or active stock workflows. |
| 7. Backup and restore pass | **VERIFIED** in a real browser between independent disposable apps; inventory/layout/assets/settings persisted and target restarted. | Pass for tested normal restore. |
| 8. Authentication and essential security controls pass | **VERIFIED** for setup/login/logout/CSRF and selected roles; router CSRF regressions pass. Full security audit is out of scope. | Pass for essential tested scope; no broad security certification implied. |
| 9. No unresolved critical inventory-data defects | **No critical defect demonstrated** in audited flows; full application security/data-loss proof is not claimed. | Pass, evidence-bounded. |
| 10. Physical LED Locate cannot accidentally activate when excluded | **PASS — default-off server-side guard completed in `c3fa25d`;** inventory and scanner browser checks explicitly set `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false` and observed zero mock state writes. | Pass for the default-off guard; no physical hardware claim. |
| 11. Dual-format scannable labels work for every inventory entity | **VERIFIED (Task 3):** camera-free ZXing decoding proved QR and Code 128 images encode identical cabinet, drawer, bin and product identities; UI label pages exposed both image types and print actions. Existing manufacturer barcodes resolved independently; `wledger:` manufacturer-barcode collisions are rejected. | Pass for tested identity/label scope. |
| 12. Scan-driven stock movement is safe and contextual | **VERIFIED (Task 3):** disposable browser scanned source via keyboard wedge and product, cabinet, drawer and destination bin via camera callback; transfer cancellation preserved quantity and confirmation moved all seven units. Invalid input stayed in the dialog; WLED output was disabled and zero controller requests were recorded. | Pass for tested scan-transfer scope. |

The twelve functional gates are PASS for the stated, evidence-bounded inventory-only scope. The default-off WLED guard (row 10), dual-format scanning and scan-driven transfer (rows 6, 11 and 12), and image/stock lifecycle (rows 2, 4 and 5) are completed checkpoints. Final v1.0 acceptance still requires release-candidate sign-off and the project publication/independent-review checkpoints. A PASS here does not authorize deployment or physical WLED operation.

## 11. Verification run for this audit

All commands used the already-installed Go 1.25.5 toolchain, Node 22 and local Chromium. Browser applications/databases/uploads and controller mocks were disposable/loopback-only.

| Command | Result |
|---|---|
| `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go test -count=1 -tags fts5 ./...` | **PASS** — full Go suite. |
| `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go test -race -count=1 -tags fts5 ./...` | **PASS** — full race suite. |
| `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go vet -tags fts5 ./...` | **PASS**. |
| `CGO_ENABLED=1 /home/spetchal/gotoolchain/go/bin/go build -tags fts5 ./cmd/server ./cmd/mcp-server` | **PASS**. |
| `npm test` | **PASS** — all 20 grid-painter behavior tests. |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go CHROMIUM_PATH=/bin/chromium npm run test:csrf-browser` | **PASS** — setup/auth, multipart part/image, stock increment, settings/user/backup and CSRF negatives; no unexpected page/console errors. |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go CHROMIUM_PATH=/bin/chromium npm run test:layout-browser` | **PASS** — layout/controller/wiring/allocation and mock Locate/Stop, physical-first no-WLED route, and scanner stub; zero page/runtime errors in the reported scenarios. |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go CHROMIUM_PATH=/bin/chromium npm run test:task065-restore` | **Harness exited 0; overall label remains PARTIAL by design.** Fresh install, normal mock Locate/Stop, actual disposable-server SIGKILL recovery, separate-app UI backup/restore, restart and startup journal rollback/roll-forward assertions passed. Expected console entries were deliberate invalid-port 400s and requests interrupted by test SIGKILL. |
| `git ls-remote origin refs/heads/development/chatgpt-m6-m7` | **PASS** — remote branch equals starting SHA `526997fa11d828c92800c99e76d71bc6433bfb99`. |

The 067B delayed-delivery failure reproducer and 067D host firmware prototype were **not rerun**; their recorded evidence was reviewed. No experimental firmware build was started.

## 12. Audit conclusion and next implementation task

**Can WLEDger be used for inventory today?** Yes, for the browser-tested inventory workflows on this development baseline. It is not yet accepted as a v1.0 release.

**What blocks v1.0?** For the recommended inventory-only profile: a missing server-side WLED write fence; physical cabinet/drawer QR routes that do not target the new physical entities; and a small, explicit image/stock lifecycle acceptance gap (with image removal absent from the form). There is no demonstrated critical stock-corruption defect in the tested paths.

**Exact next implementation task:** add a **default-off, server-enforced inventory-only WLED control gate** covering all WLED state mutations (physical and legacy Locate, flash/write, GlobalOff), with negative browser/router tests proving no loopback mock receives a state POST when the gate is off. Preserve only an explicitly specified safe recovery-cleanup path for pre-existing durable ownership. Do not add a WLED fencing architecture, touch production configuration, or contact hardware as part of that task.

No production source, schema, migration, deployment or hardware was changed or used.


## 13. Task 2 follow-up — physical QR navigation

This follow-up records the bounded implementation requested after the audit. It updates the physical QR finding only; it does not mark the entire v1.0 release gate complete. The implementation started from `c3fa25d76c29262a43e4330cb374f535928f602f` on `development/chatgpt-m6-m7` and adds no schema or migration.

- Physical cabinets use `wledger:physical-cabinet:<id>` → `/storage/cabinets/<id>`.
- Physical drawers use `wledger:physical-drawer:<id>` → `/storage/drawers/<id>`.
- Physical bins retain the stable `wledger:bin:<id>` identity → `/parts?bin=<id>`; direct bin filtering now rejects malformed and missing IDs consistently with scan resolution.
- Newly generated part QRs use `wledger:part-id:<id>` → `/parts/<id>`. Existing `wledger:part:<barcode>` labels remain readable.
- Legacy `wledger:cabinet:<id>` controller codes and `wledger:drawer:<id>` container codes retain their original `/hardware/<id>/grid` and `/drawers/<id>` destinations.
- The browser acceptance on a fresh disposable app decoded physical cabinet, drawer, bin, part, legacy controller and legacy container QR PNGs with a camera-free OpenCV decoder, then submitted every decoded value through the real scanner success callback and `/scan` routing. Correct destinations and identities passed. Deleted, malformed and unknown identities returned graceful 404/400 responses. Physical WLED output was disabled and the test recorded zero WLED requests.

The QR navigation finding is resolved for this tested scope. Image removal and stock-lifecycle browser acceptance remain outstanding; the next planned work is that bounded inventory acceptance, followed by the final v1.0 release gate. No production/staging database or physical controller was accessed.

## 14. Task 3 follow-up — dual-format scanning and contextual stock transfer

Task 3 adds the owner's v1.0 scanning clarification without changing the database schema, WLED controller behavior, firmware, or output defaults.

- All four physical inventory entity types use their existing stable scan identity for generated QR and Code 128 images. Printable UI labels include both symbologies; the product sheet includes products with or without manufacturer barcodes.
- `/scan/resolve` shares the `/scan` identity interpretation and reports typed, validated entity IDs for active operations. Legacy electrical controller/container prefixes remain distinct. `wledger:` is reserved for internal scan identities to prevent manufacturer-barcode collisions.
- The active Move Stock dialog consumes scanner events rather than navigating. It validates source bin and product, narrows the existing picker after physical cabinet/drawer scans, selects the destination bin, and requires submit plus explicit confirmation. Manual picker selection remains available. Keyboard-wedge terminators are suppressed after a valid scan so focused controls cannot submit or navigate unexpectedly.
- The browser harness decoded generated QR and Code 128 images with ZXing, used the real print-label UI, tested camera callback and keyboard-wedge injection, verified transfer cancel/confirm quantities, rejected malformed identifiers, and recorded `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false` with zero WLED requests.
- Verification for this checkpoint: full Go suite, race suite, `go vet` and server/MCP builds; `npm test`; `npm run test:layout-browser`; `npm run test:csrf-browser`; the Task 064 fresh-install harness (exit 0 with its existing `PARTIAL` result for unrun recovery/restore/multi-process evidence); `npm audit --omit=dev` (0 production dependency vulnerabilities); and `git diff --check`. A full `npm audit` reports five high-severity advisories in existing development-tool transitive dependencies (`braces`, `picomatch`, `source-map-js`); the new ZXing test decoder does not affect production dependencies.

The dual-format scanning and scan-driven transfer gaps are resolved. The release-candidate acceptance and owner sign-off remain; this checkpoint does not enable or contact physical controllers.

## 15. Task 4 follow-up — complete inventory lifecycle acceptance

The authorized follow-up started from `bbec5a54f6895e7f93a6e82042c8e8da7bd4ec6c` on `development/chatgpt-m6-m7`. It closes the image-removal gap and the remaining browser lifecycle acceptance without changing schema, scanner identity architecture, QR/Code 128 behavior or WLED output policy.

- Part edit now offers an explicit remove-image checkbox. Replacement upload takes precedence if both controls are selected. The service persists the cleared image reference in the existing update transaction and deletes the previous owned image and thumbnail only after transaction success; failed updates preserve the database reference and files. Existing upload validation and path handling remain unchanged.
- Disposable service/browser coverage proves initial upload, replacement, editing without an image change, successful removal, failed removal/update preservation, duplicate product rejection, invalid stock assignment non-mutation, quantity increase/decrease, cancelled and confirmed manual move, stock-assignment cancel/removal, product deletion with its remaining assignment, physical-bin and product search, and restart persistence.
- The actual browser lifecycle and separate-app backup/restore run with `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false`. The source and destination are fresh disposable apps/databases/uploads; image/thumbnail files, parts, bins, assignments and quantities match after restore/restart. Loopback mock WLED endpoints received no state writes.
- The independent scanning regression decoded QR and Code 128 identities for physical cabinets, drawers, bins and products, checked distinct legacy electrical controller/container identities, and exercised cancelled/confirmed scan-driven transfers with output disabled. Existing scanner modal regression also passed.
- Final task verification: full Go tests, full race tests, `go vet`, server/MCP builds, JavaScript tests, inventory/restore browser acceptance, QR/Code 128 scan browser acceptance, scanner modal regression, CSRF browser acceptance, and `git diff --check` passed. No production/staging service, database, or physical controller was contacted.

The next step is release-candidate acceptance and owner sign-off. This completion does not itself constitute a v1.0 release, merge, deployment or physical WLED authorization.
