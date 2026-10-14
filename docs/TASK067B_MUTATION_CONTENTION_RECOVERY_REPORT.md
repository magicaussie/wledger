# Task 067B — Mutation Guards, SQLite Contention and Recovery Ownership

- **Repository:** `magicaussie/wledger`
- **Branch:** `development/chatgpt-m6-m7`
- **Starting commit:** `28ee6d8a8f95bd832abc3b29b63c33707a4c5b2d`
- **Ending revision:** scoped tests and this report verified atop the starting commit; the published Task 067B SHA is recorded in the completion response and branch head.
- **Overall acceptance:** **PARTIAL / BLOCKED** — mutation guards and bounded lock handling pass for the exercised service cases; the delayed-in-flight activation scenario is **FAIL** because pixels relight after recovery has recorded `stopped`.
- **Physical WLED:** not contacted. **Do not begin controlled hardware testing.**

## 1. Baseline and isolation

`HEAD` and `origin/development/chatgpt-m6-m7` matched the requested start SHA. The only pre-existing working-tree changes were the explicitly protected `docs/OMO_HANDOVER.md`, `.task055b-staging/`, and `docs/TASK056_FUNCTIONAL_AUDIT.md`. They were preserved and excluded from Task 067B staging. No additional pre-existing changes were present at initial inspection.

I reviewed `AGENTS.md`, the Task 067 acceptance report, `internal/wled/physical_locate.go`, `internal/db/coordination.go`, `internal/db/store.go`, SQLite DSN setup, controller and layout mutation services, and recovery startup/timer behavior. No application source, schema, query, or template change was made. The Task 067B changes are tests and this report.

Tests use fresh disposable SQLite databases, synthetic cabinets/drawers/bins/stock, and loopback mocks only. Go WLED mocks use `httptest` loopback listeners with RGB framebuffers. The lock-holder tests use a separate child OS process on the same disposable file DB. There are no production/staging DB or service accesses, and no physical controller requests.

## 2. Mutation guard matrix

The new storage/hardware tests exercise service APIs against durable database session/target rows in each applicable state. They are **service-level tests**, not HTTP status tests. For every rejected service mutation, the tests compare controller/layout/wiring/allocation/attachment/target snapshots before and after; the physical layout test also compares the inventory assignment ID and quantity. This matrix does not itself issue WLED requests; the calls under test are configuration services, and the existing Task 067 independent-server test provides the separate live-process WLED evidence noted below.

| ID | Mutation | Result | Evidence and limits |
|---|---|---|---|
| M01 | Controller IP | **PASS — service level** | Update rejected in `activating`, `active`, `stopping`, `partial`, and `stopped` with unresolved target marker. Endpoint and saved target unchanged. One Task 067 browser case also rejected IP edit from the second real server after the owner process died; the original owner then cleaned the saved ranges. |
| M02 | Controller port | **PASS — service level** | Same five persisted ownership conditions; port and target snapshot unchanged. No distinct real-server port-edit race was run. |
| M03 | Controller deletion | **PASS — service level** | Target deletion refused in all five conditions. Deletion of a different controller is also refused while global physical ownership is unresolved. |
| M04 | Controller reassignment | **PASS — service level** | Cabinet controller membership mutation with changed membership rejected in all five conditions. |
| M05 | Cabinet-controller membership | **PASS — service level** | Rejected with no row changes in the same state matrix. |
| M06 | Controller order | **PASS — service level** | Reorder rejected; original membership slots preserved. |
| M07 | LED string configuration | **PASS — service level** | Wiring service edit rejected; string rows and electrical revision unchanged. |
| M08 | Wiring/path section | **PASS — service level** | Path edit rejected; original path section and owning target snapshot unchanged. |
| M09 | Drawer LED allocation | **PASS — service level** | Allocation replacement rejected; original allocation and target range/commands unchanged. |
| M10 | Bin LED mapping | **NOT APPLICABLE** | The physical-bin model has no per-bin LED map. Physical LED ownership is represented by drawer allocations and controller targets. Legacy bin `LedIndex` belongs to electrical-container mapping and is intentionally separate; no unsupported API was invented. |
| M11 | Physical bin reposition | **PASS — service level** | Layout save/reposition rejected; geometry, layout revision and inventory assignment unchanged. |
| M12 | Physical bin detach/move | **PASS — service level** | Both attachment mutations rejected; attachment, bin identity and stock assignment unchanged. |
| M13 | Cabinet delete/archive | **PASS — service level** | Both operations rejected without changing either cabinet. Drawer archive/delete are additionally covered by the physical layout matrix. |
| M14 | Controller name-only edit | **PASS — service level** | Rename is allowed in every tested state; endpoint, port and saved target are unchanged. Subsequent endpoint/port changes remain rejected. |

The matrix seeds persisted state rows in supported states and a persisted target with `may_have_received=1`; this makes the guard states deterministic and is not a fabricated API/state. The service code is the evidence boundary. Actual HTTP response mapping for this complete 14-row matrix was **NOT TESTED**. The Task 067 browser process test separately proved one IP rejection and original-range recovery, but did not submit every mutation through two live server processes.

No configuration mutation changed the saved target in these tests. An end-to-end follow-up Stop after each individual mutation rejection was **NOT TESTED**; the published Task 067 browser regression covers that sequence for its single IP-edit case only.

## 3. Concurrent mutation races

| Race | Result | Evidence |
|---|---|---|
| A Locate vs B controller IP/port/wiring/allocation edit through two independent server processes | **PARTIAL** | Service-level guards and independent-process SQLite lock tests pass. The full live two-server race matrix for these four mutation routes was not implemented. |
| A Stop vs B controller configuration edit through two independent server processes | **PARTIAL** | Stop-under-contention service test and controller-edit-under-contention service test pass using separate OS claim-holder processes. Not an HTTP-server-to-HTTP-server race. |
| A process crashes while B attempts conflicting mutation | **PARTIAL** | SIGKILL claim-release behavior plus durable ownership refusal is covered at service/database level. The prior Task 067 actual-server test covers owner-process SIGKILL, a post-crash IP edit rejection, and saved-range recovery, but not a configuration request synchronized against the in-flight crash boundary. |
| Both server processes edit the same controller concurrently | **NOT TESTED** as two live server processes | SQLite claim serialization is exercised by a separate-process writer holder and one controller service caller; not two HTTP mutation handlers racing. |

Existing Task 067 evidence remains valid for two separately running WLEDger server processes sharing one DB: conflicting Locate and foreign Stop did not add WLED writes; after a SIGKILL, the second process could not edit the unresolved endpoint or recover as another user; the original owner’s restarted process sent the persisted targeted cleanup. That is not a substitute for the unrun mutation race matrix above.

## 4. SQLite contention matrix and timing

The default database DSN in `db.Open` sets WAL mode and `_busy_timeout=5000`. The new deterministic contention tests set `PRAGMA busy_timeout=300` on the contender connection so repeated test cases finish quickly; they assert a generous 2-second maximum per operation and verify that the callback did not get through to a state POST or partial configuration change.

A distinct child process signals after it has acquired `BEGIN IMMEDIATE` and holds the transaction until the parent sends SIGKILL. The contender is a service call running in the test process on the same file-backed DB. Therefore this is **cross-process SQLite/service evidence**, not an independently running HTTP-server pair.

| ID | Condition | Result | Measured result / assertions |
|---|---|---|---|
| C01 | A holds immediate claim; B requests Locate | **PASS — service level** | Two requests each failed at the 300 ms busy timeout: **300.75 ms**, **300.79 ms**. No new session, controller preflight/state POST, or activation. |
| C02 | A holds claim; B requests Stop | **PASS — service level** | With active Locate ownership, two Stops timed out at **301.16 ms**, **301.43 ms**. No cleanup POST; active/partial ownership and target marker remained. |
| C03 | A holds claim; B edits controller endpoint | **PASS — service level** | Update returned `database is locked` after **300.86 ms**; controller row and saved recovery endpoint were unchanged. After A died, retry reached the durable ownership check and was rejected with `ErrPhysicalLocateActive`. |
| C04 | A crashes while holding SQLite writer lock | **PASS — subprocess** | Claim holder was verified terminated by `SIGKILL`; the contender subsequently acquired the claim. Existing `TestOperationClaimReleasedOnProcessTermination` also passes. |
| C05 | Repeated competing Locate requests | **PASS — service level** | Two attempts under the single held claim were bounded and emitted no WLED writes. This is not a high-concurrency load test. |
| C06 | Repeated Stop requests during contention | **PASS — service level** | Two attempts retained ownership and emitted no cleanup until lock release; after release, targeted Stop and a repeat Stop completed. |
| C07 | SQLite busy timeout | **PARTIAL** | Verified `db.Open` default is 5000 ms; exercised controlled contention at PRAGMA 300 ms with measured bounded returns. Did not wait for or measure a full default 5-second busy timeout. |
| C08 | Successful operation after lock release | **PASS** | Locate and Stop succeeded after the lock-holder process was killed; original marker was resolved only after targeted acknowledgements. |

No retry loop/storm was introduced: each retry is an explicit test call. SQLite failures propagated as errors; no unclaimed WLED write occurred. The small controlled contention tests do not establish overall throughput or behavior on unsupported network filesystems.

## 5. Recovery authority and operator procedure

Source review found no administrator takeover or “recover for another user” HTTP/service API. `StopDrawer` checks `session.OwnerID` against the authenticated caller and returns `ErrLocateSessionOwned` on mismatch. The tested foreign user is rejected without a WLED POST and without changing ownership.

There is an existing **background recovery** path: on service construction, durable sessions are loaded and timers are scheduled; the internal retry uses the persisted owner ID as a worker argument and the same durable SQLite claim/targeted cleanup path. It is not an authenticated user takeover and does not replace the persisted `OwnerID`. The new service-level test verifies:

1. A different owner cannot Stop the session.
2. A restarted service worker finds the durable session while the mock controller is offline.
3. State remains `partial` and the target marker remains `1` while offline.
4. Once the original loopback controller reconnects, the background worker sends targeted cleanup and clears ownership only after acknowledgement.

This worker/restart test constructs a new service instance in one Go test process; the prior Task 067 browser suite separately exercised a real application-process restart and owner-initiated Stop. An independent server process performing background recovery with the original web user unavailable is **PARTIAL / NOT TESTED end-to-end**.

**Operator guidance supported by current behavior:** do not delete session rows or change controller endpoints/topology. Keep the durable session; restart WLEDger or allow its scheduled retry to run. If the original endpoint is offline, restore network/controller availability at that same saved endpoint and allow the background worker to retry. If endpoint or mapping identity differs, the service fails closed and keeps the target unresolved. There is no immediate authenticated-admin retry affordance for an owner who cannot sign in.

**Smallest explicit administrative recovery proposal (not implemented):** add a separate audited admin “retry cleanup” action, not a takeover. Authorize the user as an administrator at the handler; record the operator ID in an audit event separate from the durable Locate `OwnerID`; invoke the existing recovery routine against the saved session ID and immutable target snapshots. It must not start a new Locate, rewrite target endpoint/ranges, or transfer ownership. It may transition to stopped only after every saved targeted cleanup is acknowledged and persisted; offline/stale-identity failures retain ownership. Security/API design review is required before implementation.

## 6. Delayed in-flight HTTP command — safety failure

`TestPhysicalLocateDelayedActivationCanRelightPixelsAfterAcknowledgedRecovery` uses a real child process running the Locate service and a loopback WLED mock. It applies deterministic barriers rather than relying on delivery sleeps:

1. The mock receives the complete targeted activation but waits before applying it to the framebuffer.
2. The DB contains an `activating` session and durable `may_have_received=1` marker.
3. The child service process is SIGKILLed; the mock keeps the already-received request pending independently of the disconnected client.
4. A restarted service verifies the saved controller/mapping, sends targeted cleanup and receives success acknowledgements. Durable state becomes `stopped`, marker `0`; the framebuffer has owned ranges black and unrelated RGB values unchanged.
5. The test releases the mock barrier; it applies the already-received activation **after** cleanup acknowledgement.

| Scenario | Result | Evidence |
|---|---|---|
| Delayed activation applied after acknowledged cleanup | **FAIL — reproduced safety limitation** | Session remains `stopped`, target marker remains `0`, yet controller framebuffer is relit. The delayed request was `{"seg":[{"i":[96,100,[128,0,0]],"id":10}]}`. Cleanup acknowledgements preceded it; cleanup covered the persisted owned ranges, including segment 10 `[96,100)` and the other saved ranges. No GlobalOff was used. |
| Unrelated pixels | **PASS in this scenario** | The mock framebuffer is unchanged outside the delayed activation’s persisted owned range. |
| Immediate second Stop repairs it | **NOT TESTED as an operator path** | Once the durable session is terminal, `StopDrawer` no-ops; the reproducer intentionally does not erase the evidence or pretend the session still owns the pixels. |

This is not an application fix. It violates the required safe-recovery outcome: a command already accepted by the controller can apply after recovery has released durable ownership. SQLite transactions serialize WLEDger writers but cannot revoke a packet/request accepted by the controller. **Do not solve this with an arbitrary quiet-period delay.**

Options requiring protocol/design review:

1. A controller-side generation/fencing token or idempotency sequence that rejects stale commands; ordinary WLED JSON state writes currently provide no such mechanism.
2. A supported controller-side command drain/queue barrier plus state verification, if WLED firmware can provide a reliable guarantee.
3. A bounded-delivery/quiet-period policy only if an independently established maximum in-flight delay exists; absent such a bound it is risk reduction, not proof of safety.
4. A dedicated hardware command owner/proxy can reduce competing senders but still cannot retract an accepted delayed command unless the controller provides ordering/fencing semantics.

No mitigation was implemented. The delayed-command protocol, ownership release point, and any controller firmware/proxy requirement need owner/architecture review before further implementation.

## 7. Defects, fixes and limitations

- **Defect demonstrated:** durable ownership is released after a cleanup acknowledgement even though a previously transmitted activation may still apply later. This is an observed protocol limitation with unsafe final framebuffer evidence.
- **Application-source fixes:** none. The issue requires architectural/controller-protocol analysis; no speculative timing fix was attempted.
- **Regression evidence:** test-only delayed-delivery reproducer remains in the suite and passes by asserting the observed unsafe ordering. Its test-pass status is not a safety PASS; the scenario verdict is FAIL.
- **Mutation coverage boundary:** full per-operation checks are service-level; HTTP route response codes and all live-server races remain incomplete.
- **Recovery ownership boundary:** owner identity is not transferred; automatic internal retry is available, but no administrator retry UI/API exists.
- **External writers:** no WLED hardware or external WLED writer was tested; SQLite cannot coordinate them.

## 8. Verification gates

| Gate | Result |
|---|---|
| Changed Go files `gofmt` | **PASS** — all Task 067B Go files clean. |
| Repository formatting comparison | **PASS with baseline exception** — exactly the existing 42 unformatted Go files; same set as start; no new finding. |
| `templ generate` | **PASS**, `updates=0`. |
| `sqlc generate` | **NOT APPLICABLE** — no SQL schema/query/generated binding changes. |
| `go test -count=1 -tags fts5 ./...` | **PASS**. |
| `go test -race -count=1 -tags fts5 ./...` | **PASS**. |
| `go vet -tags fts5 ./...` | **PASS**. |
| Server, MCP-server, wall-browser builds (`-tags fts5`) | **PASS**, output binaries in `/tmp`. |
| `npm test` | **PASS**, all 20 tests. |
| CSRF browser regression | **PASS**. |
| Layout browser regressions | **PASS**. |
| Task 064 fresh-install browser suite | **PASS as a harness**, its overall historical verdict remains PARTIAL. |
| Task 065/066A restore/startup recovery suite | **PASS as a harness**, broader Task 065 report remains PARTIAL. |
| Task 067 fault-injection/browser suite | **PASS for executed cases**, overall earlier report remains PARTIAL. |
| New Task 067B mutation/contention/recovery tests | **PASS as tests**; delayed-delivery acceptance is **FAIL** as recorded above. |
| `node --check web/tests/task064_fresh_install.browser.test.js` | **PASS**. |
| `git diff --check` | **PASS**. |

The test-runner Go path was supplied explicitly as `/home/spetchal/gotoolchain/go/bin/go`; Chromium path was `/bin/chromium`. No packages were installed.

## 9. Deployment and physical-testing decision

**Deployment blocker:** delayed activation can relight owned pixels after cleanup acknowledgement and after durable ownership is marked stopped. This is a safety failure, not merely a missing acceptance case. Full two-server HTTP races and a real-server ownerless-background-recovery case also remain incomplete.

**Recommendation: NO — controlled physical WLED testing must not begin.** Keep validation restricted to loopback mocks until the delayed-command fencing/ordering question is resolved by owner/architecture review, a safe protocol is approved and implemented, and the mutation/HTTP race gaps are accepted. This report is not production-readiness evidence.
