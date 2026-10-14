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

The smallest credible v1.0 boundary is **Profile A — inventory-only**, after one release-specific guard is added: all WLED state-changing actions must be disabled server-side by default for that profile, not merely hidden in the UI. Two other bounded v1 acceptance gaps remain: image removal is not offered in the current part form; and current “cabinet/drawer” scan codes route to legacy controller/container records rather than the new physical cabinet/drawer entities. Close those and run the finite final gate below. Do not require custom WLED firmware work for inventory-only v1.0.

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
| **P0** | **Ship an enforceable inventory-only WLED boundary.** Source review: WLED write routes remain registered with no release profile guard (`internal/router/router.go`, especially the hardware action group). | Add a default-off server-side capability/profile guard for all state-changing WLED paths: modern/legacy Locate, legacy flash/write and GlobalOff. Hide/omit corresponding controls. Define narrowly how targeted recovery cleanup for existing durable ownership is handled; do not permit a new activation. Do not change unrelated WLED architecture. | Browser/API tests with a disposable DB and loopback mocks: with default Profile A, submit every registered WLED activation/global-off route including direct POSTs; all reject before any mock state POST. Confirm normal inventory operations still pass. If cleanup exception is retained, seed one durable unresolved session and assert only saved targeted black commands can be sent; no GlobalOff. Verify a separately opted-in test profile can exercise the existing mock suite, without enabling physical WLED. | **0.5–1.0 day** | None. Do first; no real hardware. | **YES — release gate.** |
| **P1** | **Complete physical cabinet/drawer QR navigation.** Current cabinet/drawer prefixes route to controller/container entities, while v1 uses physical `cabinets`/`drawers`; there is no physical QR route. | Add stable physical cabinet/drawer QR identity and routes without renaming/reusing legacy electrical containers. Preserve existing legacy prefix behavior or explicitly version/alias it. Include exact physical bin/part destinations in the same acceptance journey. | On a fresh app, create physical cabinet → drawer → bin → part entirely through UI. Generate each supported QR, decode with an in-test/camera-free decoder or inject the exact decoded code into the real scanner callback, navigate in Chromium, and assert the final cabinet/drawer/bin/part page and selected identity. Keep controller/container legacy scans distinct. | **1–2 days** | Agree route/code identity compatibility in the implementation; no schema redesign. | **YES — QR navigation is an explicit v1 requirement.** |
| **P1** | **Finish inventory image and stock lifecycle acceptance.** Initial upload/edit and quantity adjust/move passed; image removal is absent in UI; image replace, remove-stock, part-delete and failure non-mutation are not browser-proven. | Add explicit safe image removal if retained in scope; otherwise owner must explicitly remove it from v1 acceptance. Extend the disposable browser path for replacement/removal, stock-assignment removal, part deletion and invalid/duplicate submissions. Change application code only for a reproduced defect. | Browser confirms replacement updates the displayed image and stored file reference; remove clears the reference and removes owned files only after successful persistence; failed update leaves prior image/files intact. Stock removal deletes only the selected assignment and expected quantity, and persisted quantities for other bins are conserved. Part deletion/invalid forms give actionable feedback and no partial DB/upload mutation. Cover basic search after these changes. | **1–2 days** | P0 guard can proceed independently. | **YES for the explicit requested image-removal/stock-removal acceptance.** |
| **P1 — final gate (no new feature task)** | Run one release-candidate pass after the above changes; current reusable suites already cover fresh install, separate restore, CSRF and layout. | Freeze v1 inventory-only scope and document the default WLED-disabled deployment setting/guarantee, backup procedure and exact supported QR identities. No new roadmap or numbered workstream. | Complete the Definition of Done below from a clean disposable install; run Go tests/race/vet/build, JS and browser regressions; inspect exact changed-source diff; verify only loopback mocks were contacted and no production/staging paths were used. | **0.5–1.0 day** | Backlog items 1–3. | **YES — release sign-off.** |

**Shortest practical route:** complete the P0 write fence, then the two bounded P1 UI items, then one release-candidate run. Estimated total: **3–6 focused working days** (0.5–1 + 1–2 + 1–2 + 0.5–1). This estimate is grounded in the existing passing browser harnesses and the direct source gaps above; it is not a promise. If image removal or physical QR behavior causes an unanticipated data-model change, stop and revise the estimate rather than expand scope silently.

## 10. Release definition of done — Profile A

Status below distinguishes observed evidence from proposed completion acceptance.

| Gate | Current evidence | Release status |
|---|---|---|
| 1. Fresh install succeeds | **VERIFIED**: current disposable browser app started from empty DB; migrations 1–20 applied; setup completed. | Pass. |
| 2. Core inventory workflows pass in real browser | **VERIFIED for create/edit, physical bin creation/placement, stock assignment/adjust/decrement/transfer, basic search.** Drawer reorder, stock removal and part delete remain incomplete acceptance. | Partial; backlog item 3 + final gate. |
| 3. Data survives application restart | **VERIFIED**: browser-created installation snapshot matched before/after restart. | Pass. |
| 4. Stock operations preserve quantities | **VERIFIED** for increase/decrease/move; move preserved quantity and restart snapshot matched. Assignment removal remains untested. | Partial; backlog item 3. |
| 5. Images work | **VERIFIED** for initial upload and separate backup/restore asset. Replacement is service-tested but not browser-tested; remove is absent. | Partial; backlog item 3. |
| 6. QR navigation works | **PARTIAL**: PNG routes and selected legacy scan redirects are tested; modern physical cabinet/drawer scan path not implemented or verified. | Fail for full requested physical hierarchy until item 2. |
| 7. Backup and restore pass | **VERIFIED** in a real browser between independent disposable apps; inventory/layout/assets/settings persisted and target restarted. | Pass for tested normal restore. |
| 8. Authentication and essential security controls pass | **VERIFIED** for setup/login/logout/CSRF and selected roles; router CSRF regressions pass. Full security audit is out of scope. | Pass for essential tested scope; no broad security certification implied. |
| 9. No unresolved critical inventory-data defects | **No critical defect demonstrated** in audited flows; full application security/data-loss proof is not claimed. | Pass, evidence-bounded. |
| 10. Physical LED Locate cannot accidentally activate when excluded | **NOT MET**: WLED write routes are currently enabled in the source/router; no Profile A guard exists. | Fail until backlog item 1. |

The release is accepted only when all ten rows are PASS for the stated scope and the release artifacts are published/reviewed under the project policy. A PASS here does not authorize deployment or physical WLED operation.

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

No production source, schema, migration, deployment or hardware was changed or used in this audit.

## 13. Task 2 follow-up — physical QR navigation

This follow-up records the bounded implementation requested after the audit. It updates the physical QR finding only; it does not mark the entire v1.0 release gate complete. The implementation started from `c3fa25d76c29262a43e4330cb374f535928f602f` on `development/chatgpt-m6-m7` and adds no schema or migration.

- Physical cabinets use `wledger:physical-cabinet:<id>` → `/storage/cabinets/<id>`.
- Physical drawers use `wledger:physical-drawer:<id>` → `/storage/drawers/<id>`.
- Physical bins retain the stable `wledger:bin:<id>` identity → `/parts?bin=<id>`; direct bin filtering now rejects malformed and missing IDs consistently with scan resolution.
- Newly generated part QRs use `wledger:part-id:<id>` → `/parts/<id>`. Existing `wledger:part:<barcode>` labels remain readable.
- Legacy `wledger:cabinet:<id>` controller codes and `wledger:drawer:<id>` container codes retain their original `/hardware/<id>/grid` and `/drawers/<id>` destinations.
- The browser acceptance on a fresh disposable app decoded physical cabinet, drawer, bin, part, legacy controller and legacy container QR PNGs with a camera-free OpenCV decoder, then submitted every decoded value through the real scanner success callback and `/scan` routing. Correct destinations and identities passed. Deleted, malformed and unknown identities returned graceful 404/400 responses. Physical WLED output was disabled and the test recorded zero WLED requests.

The QR navigation finding is resolved for this tested scope. Image removal and stock-lifecycle browser acceptance remain outstanding; the next planned work is that bounded inventory acceptance, followed by the final v1.0 release gate. No production/staging database or physical controller was accessed.
