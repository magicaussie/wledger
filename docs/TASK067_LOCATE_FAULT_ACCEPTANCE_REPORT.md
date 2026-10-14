# Task 067 — Locate Fault Matrix and Multi-Process Safety

- **Repository:** `magicaussie/wledger`
- **Branch:** `development/chatgpt-m6-m7`
- **Starting commit:** `3c3a697f437ca8086bb6649f0c9702fef308466a`
- **Ending revision:** Task 067 acceptance artifacts verified on the starting commit plus the scoped working-tree changes below; publication SHA is recorded in the completion response/GitHub branch head.
- **Overall verdict:** **PARTIAL**
- **Physical-hardware decision:** **Do not begin controlled physical-hardware testing.** See deployment blockers.

## 1. Baseline, scope and isolation

The authorized branch was `development/chatgpt-m6-m7`; `HEAD` matched the requested start SHA. At baseline inspection, `origin/development/chatgpt-m6-m7` resolved to the same SHA. The working tree was already dirty. These paths were preserved and excluded from Task 067 staging: `docs/OMO_HANDOVER.md`, `.task055b-staging/`, and `docs/TASK056_FUNCTIONAL_AUDIT.md`. Existing dirty Locate/browser-test work was inspected and treated as Task 067 work; it was not reset, stashed, or cleaned. No Task 067 application-source or schema change was required.

Task 065 recovery evidence and the Task 066/066A restore design were read. The implementation review covered `internal/wled/physical_locate.go`, `internal/db/coordination.go`, `internal/db/store.go`, the WLED client, relevant storage/hardware mutation guards, and Locate/recovery tests. The current operation claim is SQLite `BEGIN IMMEDIATE`; persisted session and per-controller target rows hold ownership, and bounded HTTP state writes run synchronously while an operation claim is held. Configuration mutation code examined in storage/hardware also checks durable ownership under the SQLite writer claim. This verification does not establish that every route/mutation combination in the requested matrix was exercised.

All testing used fresh disposable file-backed SQLite databases, synthetic inventory/layout data, isolated app processes, and loopback-only mock controllers. The Go mock controllers used `httptest` loopback listeners; browser controllers bound specifically to `127.0.0.1` and ephemeral ports. The independent-process browser scenario shared one disposable DB and uploads directory between two independently started WLEDger server processes on distinct loopback HTTP ports. The browser and Go mocks maintained RGB framebuffers, logged state writes, and applied the received segment-relative `seg[].i` ranges. No production or existing staging installation was accessed and no physical WLED controller was contacted.

The first bare browser invocation failed because `go` was not in the shell `PATH`. Re-running with the installed toolchain explicitly supplied (`GO_BIN=/home/spetchal/gotoolchain/go/bin/go`, `CHROMIUM_PATH=/bin/chromium`) passed. This was an invocation/environment issue, not a code failure. `sqlc generate` reformatted one unrelated baseline blank line in `internal/db/physical_locate_sessions.sql.go`; that incidental generated-file-only change was restored. Final Task 067 Go files are gofmt-clean, and the 42-path repository gofmt baseline is unchanged.

## 2. Safety invariant results

| Invariant | Verdict | Evidence / boundary |
|---|---|---|
| S1 — Durable ownership before activation | **PASS** | Crash tests inspect the persisted `activating` session, target endpoint/ranges/commands, and committed `may_have_received` marker before delivery. The actual-server process test also observes the marker while the mock holds the response. |
| S2 — Uncertain delivery is not treated as unsent | **PASS** for exercised faults | Timeout, refused connection after marker, HTTP 500 after application, and lost response retain ownership unless targeted cleanup is acknowledged and persisted. |
| S3 — Cleanup is restricted to owned saved ranges | **PASS** for exercised paths | Recovery requests are asserted against saved commands; the real-server process recovery sends the original `[24,34)` and `[49,54)` ranges only. |
| S4 — Unrelated pixels preserved | **PASS** for exercised paths | Exact before/after RGB framebuffer assertions run in Go mock tests and browser tests, including pixels adjoining owned ranges. |
| S5 — No GlobalOff on fault/recovery paths | **PASS** for exercised paths | State payloads are inspected and rejected if they contain controller-wide `on`; browser recovery assertions use only targeted `seg[].i` writes. |
| S6 — Conflict protection | **PARTIAL** | Cross-process competing Locate/Stop and selected endpoint/configuration guards are exercised. Full active/activating/stopping/unresolved mutation matrix and simultaneous layout-write cases were not executed. |
| S7 — Endpoint identity retained | **PARTIAL** | One cross-process unresolved IP-change attempt returned conflict without changing the saved endpoint; Go coverage checks stale mapping/fingerprint refusal. Port, deletion, reassignment and all topology variants were not exhaustively exercised. |
| S8 — Restart persistence | **PASS** for tested crash points | Durable rows survive actual SIGKILL and restart; recovery reuses persisted target commands. |
| S9 — Multi-process ownership | **PASS** for tested conflicting requests; contention bound **PARTIAL** | Two real server processes sharing one DB did not replace ownership or issue conflicting hardware writes. Lock-wait/retry latency under intentional SQLite contention was not exhaustively measured. |
| S10 — Fail closed / actionable unresolved state | **PASS** for exercised faults | Cleanup failures retain active/partial state and the target attempt marker; successful retry clears the marker. Broader UI/operator recovery actions from a different process were not all tested. |

## 3. Pre-delivery crash matrix

`TestPhysicalLocateSIGKILLCrashMatrix` starts a child copy of the compiled Go test binary with a real file-backed disposable DB and a loopback WLED mock. The test verifies the child exited from `SIGKILL` using `syscall.WaitStatus`; ordinary returned errors are not counted as crash evidence. For L05/L06 the parent observes the mock boundary, snapshots durable rows, then kills the child. L06 is additionally exercised against a real disposable WLEDger HTTP server process in the browser harness.

| ID | Boundary and method | Result | Evidence / distinction |
|---|---|---|---|
| L01 | SIGKILL immediately before reservation transaction | **PASS** | No session/target row and no state POST. A Go service subprocess is killed at the reservation boundary; this is not a full HTTP server crash test. |
| L02 | SIGKILL immediately after reservation commit | **PASS** | `activating` session and original target snapshot remain; no activation POST. Go service subprocess. |
| L03 | SIGKILL after target snapshot persistence | **PASS** | Same atomic reservation contains controller ID/address/port, saved ranges and cleanup commands; post-crash assertions inspect them. Go service subprocess. |
| L04 | SIGKILL after `may_have_received=1`, immediately before HTTP | **PASS** | Ownership and saved cleanup commands survive; no POST was observed before recovery. Go service subprocess. |
| L05 | SIGKILL after HTTP request begins but mock has not read its body | **PASS** in service subprocess; **PARTIAL** as server-process evidence | Mock records the begun `POST /json/state` with no parsed payload; durable attempt marker remains. This is actual process termination, but the process is a child test binary exercising the service, not the web-server executable. |
| L06 | Delivery applied before acknowledgement persistence / response | **PASS** | Real disposable server received SIGKILL after loopback mock applied `{"seg":[{"i":[24,34,[0,0,128]],"id":10}]}` and withheld the response. Durable state remained `activating`, target marker `1`; restarted owner sent the two persisted black cleanup ranges. Go subprocess mock also asserts framebuffer changed before killing. |

## 4. Controller failure, timeout and recovery matrix

| Fault | Result | Evidence and limits |
|---|---|---|
| Connection refused during preflight, before reservation | **PASS** | `TestPhysicalLocateConnectionRefusedBeforeReservationSendsNoPixels`: no session, no state write, framebuffer unchanged. |
| Connection refused after attempt marker | **PASS** | `TestPhysicalLocateConnectionRefusedAfterAttemptMarkerRetainsOwnership`: marker and commands remain durable despite zero mock POSTs. |
| Connection accepted, request body not read | **PASS** | `TestPhysicalLocateTimeoutBeforeControllerReadsRequestRetainsOwnership`: bounded injected 80 ms timeout, no applied pixels, marker retained; recovered after mock availability. |
| Full command received/applied, response never returned | **PASS** | `TestPhysicalLocateControllerReceivesRequestButNeverResponds`: framebuffer application is asserted; activation and failed cleanup both retain marker until successful retry. |
| Applied command, response dropped/lost | **PASS** | Real server L06 crash test; browser mock logs applied payload and client disconnect. Recovery is targeted. |
| HTTP 500 after command application | **PASS** | `TestPhysicalLocateHTTP500AfterApplicationIsCleanedBySavedRanges`: command is applied before 500; cleanup runs against saved ranges and ownership clears only after confirmed cleanup. |
| Malformed JSON response | **PASS** for controller preflight GET; **NOT TESTED** for malformed POST acknowledgement | `TestPhysicalLocateMalformedControllerJSONFailsBeforeReservation` returns invalid JSON from `/json/info`; reservation and POST do not occur. No separate malformed JSON body response to a state POST was injected. |
| Controller unavailable during Stop, repeated offline Stop, then reconnect/recovery | **PASS** in Go mock tests | `TestPhysicalLocateRepeatedStopRetainsOwnershipUntilControllerReconnects` retains partial state/marker for repeated offline attempts, then clears saved ranges after reconnect; a repeated confirmed Stop is harmless. Not a real server-process outage test. |

## 5. Three-controller partial delivery and pixels

The supported physical Locate resolver can produce one durable session with several controller targets. No unsupported workflow was introduced. `TestPhysicalLocateThreeControllerPartialDeliveryAndRepeatedRecovery` uses three loopback mocks with nonzero initialized framebuffers. Delivery order is controller A then B then C: A acknowledges activation; B applies activation but loses its response; C is not reached. The automatic targeted cleanup confirms A, attempts B but receives injected failure, and does not contact C. Durable rows show exactly three target snapshots, A `may_have_received=0`, B `may_have_received=1`, and C `may_have_received=0`. Repeated Stop retries only the persisted B cleanup target; A is not resent and C is not falsely recorded as cleaned.

A second case, `TestPhysicalLocateThreeControllerThirdFailsBeforeApplication`, exercises the alternative later-target failure order: A and B are acknowledged; C fails before applying activation, then receives cleanup for its conservatively marked attempt. The simulator asserts all intended owned pixels are black after successful cleanup and every unrelated framebuffer pixel retains its exact initial RGB value. Both tests reject any payload containing controller-wide `on`.

The Go test mock initializes pixel `p` to `[20+p%50, 30+p%60, 40+p%70]`, applies received segment-relative RGB writes, and exposes request bodies/application outcomes. The full real-server browser run independently records the exact normal targeted payloads, including:

- activation: `{"seg":[{"i":[24,34,[0,0,128]],"id":10}]}`
- cleanup: `{"seg":[{"i":[24,34,[0,0,0]],"id":10}]}` and `{"seg":[{"i":[49,54,[0,0,0]],"id":10}]}`

The three-controller Go matrix is **PASS** for its stated orderings. It does not provide a durable external timestamped JSON evidence artifact for each Go mock request; payload assertions are made directly against the in-memory mock bodies during the passing tests.

## 6. Independent multi-process and SQLite evidence

`WLEDGER_TASK067_LOCATE=1 npm run test:task067-locate` builds one disposable server binary and starts two independent server processes against the same temporary `data/wledger.db` and `app/uploads` tree, on separate loopback application ports. Both complete startup and login. Both use the same synthetic physical layout and loopback mock controller.

1. Process A reserves a Locate. The test reads one `activating` session and one target with `may_have_received=1` while A's mock activation has been applied and its HTTP response is held.
2. Process B submits a conflicting Locate and receives HTTP 409. The session row is unchanged and there is no second state POST.
3. Process B submits Stop under its distinct authenticated owner and receives HTTP 409; no additional WLED request occurs.
4. Process A is killed with `SIGKILL`. The unresolved `activating` session and marker remain in the same DB.
5. Process B attempts an IP change to that target; it is rejected before persistence and the original endpoint remains. Process B's foreign-owner recovery is rejected and does not issue WLED cleanup.
6. Process A is restarted against the same DB and mock. Its owner submits Stop; the server emits only the saved targeted black ranges. The active session disappears only after cleanup acknowledgement. Every pixel outside `[24,34)` and `[49,54)` keeps its exact initial RGB value.

This proves no conflicting ownership/write was acquired for the tested two-process race and that process death released the in-flight SQLite claim while leaving durable pixel ownership. It does **not** constitute an exhaustive independent test of simultaneous controller edits and physical-layout changes, or a measured lock-contention retry/timeout budget. The Go DB/store suite and full package tests pass, but the specific Task 067 load/contended-writer matrix is **PARTIAL**.

## 7. Endpoint and topology mutation matrix

Results below distinguish operations explicitly injected in the Task 067 end-to-end process scenario from source-level or pre-existing unit coverage. No operation marked NOT TESTED is inferred to pass from a generic durable-session guard.

| Mutation while ownership may be unresolved | Result | Evidence |
|---|---|---|
| Controller IP change | **PASS** for one real cross-process unresolved case | HTTP conflict; DB endpoint unchanged; no WLED write. |
| Controller port change | **NOT TESTED** end-to-end | No distinct port-edit attempt during each active state. |
| Controller deletion | **NOT TESTED** end-to-end | No deletion attempt during active/unresolved process ownership. |
| Controller reassignment / cabinet-controller membership | **NOT TESTED** end-to-end | No matrix attempt. |
| Cabinet-controller membership order/change | **NOT TESTED** end-to-end | No matrix attempt. |
| LED string edit | **NOT TESTED** during unresolved ownership | No matrix attempt. |
| Wiring/path section edit | **NOT TESTED** during unresolved ownership | No matrix attempt. |
| Drawer allocation edit | **NOT TESTED** during unresolved ownership | No matrix attempt. |
| Bin LED mapping change | **NOT TESTED** during unresolved ownership | No matrix attempt. |
| Physical bin movement / attachment | **NOT TESTED** during unresolved ownership | No matrix attempt. |
| Cabinet delete/archive | **NOT TESTED** during unresolved ownership | No matrix attempt. |
| Name-only controller edit | **PASS** in Go service test | Existing hardware controller ownership test permits name-only edit; no endpoint change. |
| WLED output geometry/fingerprint change | **PASS** in Go tests | Stale mapping cleanup is withheld rather than redirected to a changed mapping. |

Accordingly, S6/S7 and the requested mutation matrix remain **PARTIAL**. Existing service methods use durable ownership checks under an immediate transaction, and the full Go suite passes, but acceptance requires explicit per-operation/per-state outcome and unchanged-row assertions not present in this run.

## 8. Repeated Stop, recovery and fail-closed behavior

- Stop twice after confirmed cleanup — **PASS**; no range expansion or controller-wide command.
- Stop racing activation — **PASS** in existing Go regression `TestPhysicalLocateStopRacingActivationCompletesTargetedCleanupInOrder`; unit/service concurrency, not independent-server browser evidence.
- Stop after timeout / lost response — **PASS**; marker retained on unsuccessful cleanup and successful retry resolves it.
- Stop after app process restart — **PASS**; real browser test restarts the same server binary/database and uses saved commands.
- Stop from another process/different owner — **PASS as safe rejection**; second process receives 409 and sends no WLED write. Automatic recovery by a different owner is not claimed.
- Partial cleanup success/failure across targets — **PASS** for the Go three-controller case; unresolved B retains marker, previously confirmed A remains resolved, never-reached C remains unattempted.
- Repeated recovery while controller remains offline, then reconnect — **PASS** in Go mock service test; no real app-process outage cycle was run for this branch.
- Recovery commands remain limited to saved ranges — **PASS** for tested cases; exact pixel preservation is checked.

A failed cleanup leaves a durable partial session with target error/attempt state rather than releasing ownership. The interface exposes the active session/Stop action for the owner. This does not establish the complete cross-process operator recovery UX for every authentication/role case.

## 9. Verification gates

| Gate | Result |
|---|---|
| Changed Task 067 Go files `gofmt` check | **PASS** — no output from `gofmt -l` for changed/new Go tests. |
| Repository Go formatting | **PASS with baseline exception** — 42 existing unformatted Go paths, same set as baseline; no new finding. |
| `templ generate` | **PASS**, `updates=0`. |
| `sqlc generate` | **PASS**; incidental formatting-only diff in an unrelated generated file was restored and is not in task changes. |
| `go test -count=1 -tags fts5 ./...` | **PASS**. |
| `go test -race -count=1 -tags fts5 ./...` | **PASS**. |
| `go vet -tags fts5 ./...` | **PASS**. |
| Server, MCP-server and wall-browser builds (`-tags fts5`) | **PASS**; outputs directed to `/tmp`. |
| `npm test` | **PASS**, all 20 grid-painter tests. |
| CSRF browser regression | **PASS**. |
| Layout browser regressions (layout editor, physical-first, barcode scanner) | **PASS**. |
| Task 064 fresh-install browser regression | **PASS as a harness**; its overall historical acceptance status remains PARTIAL. |
| Task 065 restore/crash browser regression | **PASS as a harness**; both SIGKILL recovery cases and restore scenario passed; Task 065's broader report remains PARTIAL. |
| Task 066A real-startup recovery regression | **PASS**, exercised within the Task 065 restore/crash harness; startup rollback/roll-forward recovery and zero WLED writes were asserted. |
| New Task 067 Locate fault suite | **PASS for executed cases; overall acceptance PARTIAL** — Go crash/fault/three-controller tests and two-process browser race passed. |
| `node --check web/tests/task064_fresh_install.browser.test.js` | **PASS**. |
| `git diff --check` | **PASS**. |

## 10. Defects, limitations and deployment blockers

No Locate application-source defect was demonstrated in the executed scenarios, so no production-source correction was made. The Task 067 changes are fault-injection tests, framebuffer simulation, browser orchestration and this acceptance report. The only incidental generator output was reverted.

Remaining acceptance gaps:

- The endpoint/topology mutation matrix is not exercised across every mutation and each active/activating/stopping/unresolved state.
- Concurrent controller-configuration and physical-layout HTTP mutation races between independently running server processes are not tested as explicit race scenarios.
- Busy-lock contention, bounded retry/timeout behavior and transaction outcome ordering under deliberate writer lock contention are not measured for the Task 067 multi-process case.
- Several connection/response scenarios are in-process service tests rather than real web-server processes. The report marks that distinction above.
- A malformed JSON state-POST acknowledgement and a separate lost-response-without-process-kill route case are not independently asserted (the applied/lost-response real-server crash case is tested).
- No physical firmware behavior or external WLED writer was tested. SQLite only coordinates participating WLEDger processes; it cannot fence third-party writers or revoke an HTTP command already in flight at the controller.

**Deployment blocker:** these results are not production readiness evidence. Existing Task 066 design limitations still apply: filesystem SQLite locking must be supported; all WLEDger writers/configuration paths must consistently participate in the claim; ordinary WLED HTTP has no idempotency/fencing primitive for delayed packets; external writers remain outside the database claim. Complete the untested mutation and deliberate lock-contention matrix and obtain independent source review before any controlled hardware trial.

**Recommendation:** **No — controlled physical-hardware testing should not begin on the basis of Task 067.** Keep testing limited to isolated loopback mocks until the PARTIAL cases are accepted and owner authorization is given.
