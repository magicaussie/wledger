# Task 065 — Locate Failure Recovery and Backup/Restore Acceptance

- **Date:** 2026-10-14
- **Repository:** `magicaussie/wledger`
- **Branch:** `development/chatgpt-m6-m7`
- **Starting commit:** `aebd9f7d9243bd173c3ac1c1d7577d295ef1fbfb`
- **Overall verdict:** **PARTIAL**
- **Publication:** Controlled development-branch publication is authorized for the verified Task 065 fixes and evidence. Publication does not change the **PARTIAL** acceptance verdict or authorize deployment, production/staging operation, or physical WLED use.

## 1. Baseline, scope and isolation

Initial checks confirmed that `HEAD` and the authorized branch matched the requested starting baseline. `docs/TASK064_ACCEPTANCE_REPORT.md` reported its previous application-source test baseline as `2fe170ffb9e14936db8edbff921e08279e2a6983`; it explicitly left active Locate failure/recovery and a browser restore into a separate fresh installation incomplete. Current Locate and backup/restore implementations and their existing Go tests were inspected before edits.

The initial working tree also contained pre-existing changes to `docs/OMO_HANDOVER.md` and pre-existing untracked `.task055b-staging/` and `docs/TASK056_FUNCTIONAL_AUDIT.md`. All three were preserved and excluded from this task. `docs/OMO_HANDOVER.md` was not edited.

Verification used the installed Go 1.25.5 toolchain at `/home/spetchal/gotoolchain/go/bin/go`, Node 22, and local Chromium. The host toolchain was already available, so the Go container workflow was not needed. Every browser app and SQLite database ran from a fresh OS temporary directory; source and restore destination had independent `data/wledger.db` and upload paths and applied migrations 1–19 independently. WLED mocks listened only on loopback (`127.0.0.1`) using ephemeral ports. No production database, existing staging service, physical controller, or WLED hardware was accessed.

The Task 065 browser harness used normal authenticated setup, inventory, backup-download and restore forms for data creation/export/import. SQLite reads for verification used read-only/query-only access. The Go service fixtures use direct SQL for controlled fault state; those fixtures are not used to populate the browser source or destination. Temporary browser databases, files, processes and mock servers were removed by test teardown.

The mock event log records each request's UTC timestamp, mock-controller identity, full endpoint, method, request payload (when present), response behavior and response status. The pixel simulator starts every pixel at a known nonzero colour, applies the exact received `seg[].i` commands, and checks that owned ranges are black after cleanup while every unowned pixel retains its starting colour. The complete structured browser event record was emitted by the test command; key command and state evidence is reproduced below.

## 2. Verdicts

| Acceptance area | Verdict | Basis and remaining gap |
|---|---|---|
| 1. Normal Locate/Stop | **PASS — mock only** | Fresh-install browser workflow attested five loopback controllers and exercised five drawer locations, including ranges crossing controller/segment boundaries. Captured 18 targeted POST writes: activation colours and exact black Stop ranges; no `GlobalOff`/controller-wide `on` payload. The broader failure variants remain separately classified below. |
| 2. Uncertain delivery | **PARTIAL** | Actual app-process termination after a mock applied the activation POST but before responding passed durable recovery (scenario E2). Existing Go tests cover accepted/canceled activation and bounded cleanup retry. The distinct “controller timeout before delivery” application-route scenario, with all requested state assertions captured end-to-end, was not separately run. |
| 3. Partial multi-controller delivery | **PARTIAL** | Go mock tests cover a successful target followed by failed targets, cleanup of every possibly attempted target, retention after partial cleanup failure, and retry only of unresolved targets. They do not constitute the requested three-controller A-success/B-fail/C-not-reached browser fault matrix with a retained full evidence record. |
| 4. Crash recovery | **PARTIAL** | Two real `SIGKILL` tests against disposable WLEDger processes passed: after activation was applied but before response, and during Stop after the first targeted cleanup was applied but before response. Reservation-before-first-HTTP and a crash before delivery were not separately injected. These were actual process terminations, not simulated context cancellation. |
| 5. Concurrent operations | **PARTIAL** | Go tests passed simultaneous Locate, Stop/activation interleaving, two service connections, two recovery workers, and SQLite operation-claim release after subprocess termination. Two independently running WLEDger server processes concurrently issuing web Locate/Stop against one shared file-backed database were not run. |
| 6. Endpoint/topology safety | **PARTIAL** | Go tests cover stale WLED configuration/fingerprint refusal, no cleanup to a changed mapping, active-session rejection of wiring/reassignment/capacity/mapping changes, and legacy hardware-write exclusion. The complete IP, port, delete, remap and affected-wiring mutation matrix was not exercised end-to-end during both active and unresolved recovery. |
| 7. Unrelated pixel preservation | **PASS — simulated WLED only** | Task 065 browser fault run initialized all five mock controllers with nonzero pixel colours. Exact targeted Locate, interrupted Stop and retry preserved all pixels outside the saved ownership ranges; owned pixels ended black. No payload used `GlobalOff` or controller-wide `on`. No physical hardware was tested. |
| 8. Complete backup/restore | **PASS — disposable installations** | Authenticated source UI export and destination UI restore passed between separately initialized databases/filesystems. Identities, all ten bins/attachments, parts, quantities, five controllers, memberships, strings, paths, allocations, settings and uploaded image were compared. Ten QR PNG responses matched byte-for-byte by stable bin ID. Destination migration IDs 1–19 remained applied, search/browse worked, and state persisted after destination restart. Restore sent zero WLED writes. |
| 9. Malformed backup rejection | **PARTIAL** | Go tests reject invalid ZIP structure, a truncated central directory, a corrupt manifest CRC, missing/invalid/unsupported/duplicate/ambiguous manifests, duplicate upload paths, path traversal, excessive archive-entry count and oversized archive/file/manifest/extraction declarations; rejected archive tests check unchanged DB/filesystem sentinels. The oversized multipart-upload/UI boundary and every resource limit using a fully materialized hostile ZIP were not all exercised through the authenticated browser route. |
| 10. Interrupted restore | **PARTIAL** | Deterministic database insert/transaction and filesystem-rename fault-injection tests passed, including preservation of old uploads and explicit committed-with-warning/recovery-directory outcomes. No actual process kill was injected at restore validation, extraction, database commit, or asset swap boundaries. |

**Overall remains PARTIAL.** Critical untested items include pre-delivery Locate interruption, application-level controller timeout-before-delivery, a full three-controller partial-delivery run, simultaneous independent WLEDger processes sharing one database, exhaustive controller endpoint mutations, real process termination during restore, and cross-resource database/upload atomicity.

## 3. Locate evidence and durable state

### 3.1 Real process termination tests

The opt-in command `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:task065-restore` starts a disposable server and five loopback mocks. For both crash points the test queries the same file-backed database in read-only mode, kills the disposable server with `SIGKILL`, restarts it against that database, and recovers via the normal drawer Stop form.

1. **Activation delivered, response withheld.** The mock applied controller 1’s first activation command and then held the response. The captured request was controller `mock-controller-1`, `POST http://127.0.0.1:<ephemeral>/json/state`, payload `{"seg":[{"i":[24,34,[0,0,128]],"id":10}]}`; response behavior `command applied; client disconnected before response`, status unavailable. After `SIGKILL`, the durable session remained `activating`, and its target remained `may_have_received=1`, with saved cleanup commands for segment 10 ranges `[24,34)` and `[49,54)`. Recovery sent exactly two black targeted commands to that controller and those saved ranges; acknowledgements were HTTP 200. The session became terminal and the target marker cleared only after acknowledged cleanup.
2. **Stop in progress after partial cleanup.** A second session was stopped; the mock applied `[24,34)` black and held its response. The process was killed. Durable state remained `stopping`, and the target marker remained `1`. Restart recovery replayed both saved black ranges (idempotently replaying the first), acknowledged both, then released ownership. No unrelated controller received a request.

Before each crash injection, the harness asserts there is no active/partial session and no target with `may_have_received=1`; after each killed request it records the in-flight session state and saved target command JSON; after recovery it asserts the session is `stopped` and the target marker is `0`. The browser log for the Task 065 run contained 99 mock WLED requests and 26 state POSTs with per-request timestamps, controller identities, endpoints, payloads and response behavior. The normal Task 064 fresh-install run separately captured 76 mock requests and 18 state POSTs. No mock request was sent outside the installed loopback listeners.

The normal UI Locate/Stop payloads were captured exactly as `{"seg":[{"i":[start,stop,[r,g,b]],"id":segment}]}`. Mock responses were normal JSON acknowledgements (HTTP 200); Stop changed only the corresponding ranges to black.

| Drawer/bin case | Activation payloads (`controller, segment, [start,stop), RGB`) | Stop payloads |
|---|---|---|
| Drawer 1 / bin 2 | `1,10,[24,34),[0,0,128]`; `1,10,[49,54),[0,0,128]` | Same controller/segment/ranges with `[0,0,0]`. |
| Drawer 2 / bin 3 | `1,20,[94,100),[0,0,128]`; `2,10,[0,10),[0,0,128]` | Same controller/segment/ranges with `[0,0,0]`. |
| Drawer 3 / bin 5 | `2,10,[24,34),[0,0,128]` | Same controller/segment/range with `[0,0,0]`. |
| Drawer 4 / bin 7 | `2,20,[119,125),[0,0,128]`; `3,10,[0,10),[0,0,128]` | Same controller/segment/ranges with `[0,0,0]`. |
| Drawer 5 / bin 9 | `4,20,[144,150),[0,0,128]`; `5,10,[0,10),[0,0,128]` | Same controller/segment/ranges with `[0,0,0]`. |

### 3.2 Existing focused Go evidence reviewed and rerun

The focused and full Go suites passed. Relevant assertions include:

- `TestPhysicalLocateSendsExactSegmentRelativeCommandsAndTurnsOffOnlyOwnedRanges` — exact segment/pixel/color payloads and targeted Stop.
- `TestPhysicalLocateCrossesControllersInOneDurableSession` and `TestPhysicalLocatePartialActivationAttemptsCleanupOnEveryTarget` — multi-target snapshots, attempt state and partial cleanup retention.
- `TestPhysicalLocateCleanupCommitFailureRetainsDurableOwnership` and `TestPhysicalLocateCancellationDuringAcceptedActivationRetainsOwnership` — failed persistence/cancellation keeps ownership.
- `TestPhysicalLocateStopRacingActivationCompletesTargetedCleanupInOrder`, `TestPhysicalLocateTwoConnectionsConcurrentLocateHasOneDurableOwner`, and `TestPhysicalLocateTwoRecoveryWorkersShareTargetOwnership` — operation ordering and database-backed claims.
- `TestPhysicalLocateRestartRecoveryCleanupUsesBoundedRetry`, `TestPhysicalLocateCleanupTimeoutIsIsolatedPerController`, and `TestPhysicalLocateRetriesOnlyControllerWithPartialCleanupFailure` — bounded recovery, target isolation and retry.
- `TestPhysicalLocateRejectsStaleTopologyAndPartialCommandFailure` and `TestPhysicalLocateBlocksLegacyWritersUntilConfirmedStop` — stale mapping and broad legacy writer exclusion.
- `TestOperationClaimReleasedOnProcessTermination` — a killed subprocess releases its SQLite claim while committed state remains.

These Go tests provide deterministic unit/integration fault injection; they do not replace the missing independent-server process matrix in the verdict table.

### 3.3 Exact fault-injection mechanisms and evidence boundary

- **Browser SIGKILL tests:** the mock applies the decoded targeted JSON command to its pixel model, records the request, signals the test runner, then holds the HTTP response until the client disconnects. The runner kills only the disposable WLEDger child with `SIGKILL`. It reads durable session/target state through read-only SQL after termination and restarts the same binary against the same temporary database. The initial state assertion requires no active session and zero `may_have_received=1` targets. This directly distinguishes process termination from the Go context-cancellation tests.
- **Go WLED fault tests:** `physicalMock.failPostNumber` records a command then responds `503`; `stallMethod`/`stallPath` delay a chosen mock endpoint until the request deadline; `postAccepted`/`postResponseRelease` hold a response after the mock records acceptance; wrapper stores inject transaction-commit or session-persistence failures. The Go assertions inspect durable rows and exact POST JSON, but the older unit mock's per-test `requestLog` is not itself a persisted timestamped evidence artifact. Scenarios relying only on these tests are not upgraded to PASS where the task called for actual application-process or full scenario evidence.
- **Restore failures:** malformed archive bytes are produced as invalid ZIP, truncated central directory, or stored manifest payload with a deliberately invalid CRC. Other tests use malformed/duplicate manifests and upload names, fake entry metadata for declared resource-limit checks, FK/duplicate-ID failures for SQL rollback, and `renameEntry` overrides for staged-file swap/rollback failures. These are deterministic in-process injections, not restore-process termination.

## 4. Backup/restore evidence and defects

### 4.1 Separate fresh-install restore

The source browser workflow created one 200×120 cabinet, five drawers, ten physically attached bins without legacy electrical associations, five loopback WLED controllers, ordered memberships, five physical strings with directed path sections, six drawer pixel allocations, ten parts and stock quantities, plus an uploaded part image. The source UI exported a nonempty 2.0 ZIP (`storage_data.json`, `human_readable_parts.csv`, image and thumbnail). A second process was set up independently against a fresh SQLite DB and separate `app/uploads`; its migration table was verified at versions 1–19 before restore.

Restore used the authenticated UI. Read-only snapshots of source and destination matched for stable controller/cabinet/drawer/bin/part IDs, bin attachments, controller order/configuration, wiring/path sections, allocation ranges, image path and stock quantities. The supported settings columns matched; all ten restored bin QR PNGs matched their source PNGs; the image and thumbnail were present in the destination archive/filesystem. Search and inventory browsing worked after restore and after an app restart. The restore interval produced **zero** mock WLED state writes. No direct SQL was used to populate either installation.

`TestExportRestorePreservesAllSettings` additionally uses a fresh service destination to verify non-default settings including `enable_debug_logs`, all configured colours including `color_error`, supplier cache/currency settings and `inspiration_seeds_applied`.

### 4.2 Defects found and corrected

1. **Startup recovery blocker:** the fresh-install `SIGKILL` test initially showed that a replacement WLEDger process exited during startup because `MigrateLegacyLedIndices` checked durable Locate ownership before recognizing migration 005 was already applied; `BackfillDrawerAllocations` had the same ordering issue for its completed/no-unallocated no-op. The ownership check now remains mandatory before any real conversion/backfill, but completed no-op startup work is allowed while a durable Locate awaits cleanup. `TestCompletedStartupMigrationsAreNoOpsWithDurableLocateOwnership` verifies both data migrations are no-ops and preserve the session/target marker. The application process restart/recovery browser scenario then passed.
2. **Recovery Stop unavailable in UI:** the drawer page exposed Stop for `active`, `partial` and `stopping`, but not a recovered `activating` session. The Stop form now also renders for `activating`; the real crash-recovery browser test exercises it.
3. **Settings dropped by restore:** restore omitted `enable_debug_logs`, `color_error` and `inspiration_seeds_applied`, despite exporting those `settings` fields. The restore mapping now passes those values; the SQL restore query/schema-generated binding now includes `inspiration_seeds_applied`. Both the dedicated settings round trip and separate browser restore assertions pass. No database migration was required.
4. **Corrupt/truncated archive regression:** added `TestRestoreRejectsCorruptAndTruncatedArchivesBeforeMutation`, which verifies invalid ZIP structure, a truncated central directory and a corrupt manifest CRC are rejected without changing DB/filesystem sentinels.

No unresolved application defect was observed in the scenarios that were executed. Remaining gaps below are acceptance gaps, not inferred passes.

### 4.3 Restore interruption limitation and severity

The restore implementation commits database rows before swapping uploaded files. Existing `restore_fs_test.go` fault-injection tests verify deterministic rename failures and preserve recoverable old uploads; callers receive `RestoreCommittedError` when DB changes committed but filesystem work did not complete. Database and upload replacement are **not atomic together**. A hard process/power failure between DB commit and completed upload swap can leave new database rows paired with old/missing assets or a preserved staging/recovery directory. Severity is **high operational consistency risk** and is a **deployment blocker for claiming reliable interrupted restore** until actual process-death boundaries and operator recovery instructions are accepted. Task 065 did not test those hard-kill restore boundaries.

## 5. Verification gates

All commands used the Go 1.25.5 toolchain described above.

| Gate | Result |
|---|---|
| Changed-file Go formatting | **PASS** — all changed/new Go files are `gofmt` clean. |
| Repository tracked Go formatting | **Baseline exception** — exactly the same 42 paths listed in Task 064 remain from the starting revision; no unrelated files were reformatted. |
| `templ generate` | **PASS**, `updates=0`. |
| `sqlc generate` | **PASS**. |
| `CGO_ENABLED=1 go test -count=1 -tags fts5 ./...` | **PASS**. |
| `CGO_ENABLED=1 go test -race -count=1 -tags fts5 ./...` | **PASS**. |
| `CGO_ENABLED=1 go vet -tags fts5 ./...` | **PASS**. |
| `CGO_ENABLED=1 go build -tags fts5 ./cmd/server ./cmd/mcp-server` | **PASS**. |
| `npm test` | **PASS**, all 20 grid-painter tests. |
| `npm run test:csrf-browser` | **PASS**. |
| `npm run test:layout-browser` | **PASS**. |
| `npm run test:fresh-install` | **PASS as a harness; workflow verdict remains PARTIAL**. |
| `npm run test:task065-restore` | **PASS as a harness** — normal UI restore and both actual server-kill recovery points passed; overall Task 065 remains PARTIAL. |
| `node --check web/tests/task064_fresh_install.browser.test.js` | **PASS**. |
| `git diff --check` | **PASS**. |

The repository-wide `gofmt -l` output is the same 42-file baseline recorded in `docs/TASK064_ACCEPTANCE_REPORT.md`, including `internal/db/physical_locate_sessions.sql.go`; neither new Go test nor any changed Go source adds a finding.

## 6. Remaining limitations and deployment blockers

- No physical WLED hardware was contacted; mock protocol evidence is not proof of controller firmware behavior.
- Crash/recovery was not killed at reservation-before-HTTP or before the first attempt marker. The actual-process tests cover delivery uncertain after a mock applies activation, and process death during partial targeted Stop only.
- Application-level controller timeout-before-delivery and the complete three-controller A-success/B-failure/C-not-reached matrix were not separately exercised end-to-end.
- Concurrent operations used independent SQLite connections/services and a subprocess claim-release test, but not two independently running WLEDger server processes concurrently issuing Locate/Stop against the same database.
- The full endpoint/topology mutation matrix (IP, port, delete/remap and wiring) was not browser-exercised during both active and unresolved ownership. Name-only edit behavior was not separately tested.
- Oversized multipart restore through the authenticated browser, every extraction/resource ceiling with a materialized adversarial archive, and hard process termination during restore were not executed.
- A deployment with multiple WLEDger processes sharing one uploads directory is outside the current restore lock guarantee; `restoreMu` is process-local. WLED external writers remain outside SQLite coordination.
- If a legacy installation still requires coordinate conversion/backfill while durable Locate ownership exists, the data migration remains fail-closed and startup can remain blocked until that ownership is safely resolved. The tested restart case had completed migration flags.
- Cross-resource database/filesystem restore atomicity remains the high-severity blocker described above.

**Deployment decision:** Do not claim production readiness or treat Task 065 as fully accepted. Keep overall **PARTIAL** and do not deploy these changes until the remaining critical safety cases and restore-interruption behavior are accepted. The explicitly authorized publication is limited to a scoped checkpoint on `development/chatgpt-m6-m7`; it does not imply acceptance or deployment approval. The completion response records the published commit SHA, parent SHA, changed files, verification results and GitHub synchronization status.

## 7. Task-scoped files

Task-scoped changes are:

- `docs/TASK065_RECOVERY_ACCEPTANCE_REPORT.md` (this report)
- `internal/backup/envelope_test.go`
- `internal/backup/service.go`
- `internal/backup/settings_roundtrip_test.go`
- `internal/db/settings.sql.go` (generated)
- `internal/hardware/allocation.go`
- `internal/hardware/migration.go`
- `internal/hardware/startup_recovery_test.go`
- `package.json`
- `sql/queries/settings.sql`
- `web/pages/physical_storage.templ`
- `web/pages/physical_storage_templ.go` (generated)
- `web/tests/task064_fresh_install.browser.test.js` (opt-in Task 065 process-crash and separate-install restore path)

Pre-existing protected working-tree changes remain untouched and are not included in this list or any publication.
