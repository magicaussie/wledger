# WLEDger — Full Application Code Audit

- **Repository:** magicaussie/wledger
- **Branch:** `development/chatgpt-m6-m7`
- **Review baseline:** `7270eb5976721d5e7f453f951fad42f611681a35`
- **Audit date:** 2026-10-16
- **Scope:** whole application — Go architecture and lifecycle, SQLite/sqlc/migrations, inventory and stock, cabinet/drawer/bin hierarchy, hardware and controller configuration, every LED mapping arrangement, WLED protocol and Locate/Stop safety, authentication/CSRF/input validation, templ/HTMX/Alpine frontend, backup/restore, Docker/CI, tests and docs.
- **Method:** source review of every subsystem with targeted reads and grep; end-to-end tracing of critical workflows; independent verification of each reported finding against the actual source before it was accepted. Three parallel read-only review agents were used to widen coverage; **no finding was accepted on the agent report alone** — each item below marked *Fixed* or *verified* was re-read and corrected by the lead reviewer. No live service, production/staging database, uploads directory or WLED controller was accessed. WLED work used only code inspection and the existing loopback simulators.

## How to read the status column

- **Fixed** — corrected in commit `7254815f079fe23c0be340d97c055fc3ba801826` with a focused regression test.
- **Open** — confirmed defect, not yet corrected (reason given).
- **Hypothesis** — plausible risk; a concrete failure mode was **not** established.
- **Limitation** — intentional or architectural bound, not a coding defect.

Every Go result below was produced with `go test -tags fts5`; a response that a tool "succeeded" was never treated as proof that geometry or behavior was correct.

---

## Verdict summary

| # | Severity | Finding | Status |
|---|---|---|---|
| H1 | High | Uploaded documents served inline → stored XSS; private uploads anonymously reachable | Fixed |
| H2 | High | Document filename collisions overwrite stored files | Fixed |
| H3 | High | Files deleted before the database change commits (dangling rows / lost attachments) | Fixed |
| H4 | High | Positive stock adjustment overflows and silently resets quantity to zero | Fixed |
| H5 | High | Wiring save can commit a topology the next `Load` rejects (linked string resized) | Fixed |
| H6 | High | A stale Locate writer can resurrect a superseded session | Fixed `fb88bcb` |
| M1 | Medium | First-run setup allows more than one initial administrator (race) | Fixed `76b52e7` |
| M2 | Medium | Empty/short password accepted at setup and forced reset | Fixed |
| M3 | Medium | `UpdatePartLink` not constrained to the owning part | Fixed |
| M4 | Medium | `SearchParts` returns a part more than once | Fixed |
| M5 | Medium | Proposed wiring dropped reverse/serpentine/custom transforms | Fixed |
| M6 | Medium | Multipart bodies parsed before CSRF/authorization | Fixed `463997f` |
| M7 | Medium | Restore accepts semantically invalid category graphs/specifications | Fixed `2a23a17` |
| M8 | Medium | Category images excluded from restore/startup asset validation | Fixed `2a23a17` |
| M9 | Medium | Multi-output preview labels the wrong page | Fixed |
| M10 | Medium | SIGTERM not handled → no graceful drain on container stop | Fixed |
| L1 | Low | MCP corrupts non-ASCII search queries | Fixed |
| L2 | Low | "Stopped" means cleanup acknowledged, not device-side fencing | Limitation |
| — | — | Backup snapshot skew, category write race, image decode exhaustion | Hypothesis |
| — | — | WLED segment `frz`/runtime `ledmap` not modeled in fixtures | Limitation |

No confirmed **Critical** defect was found. The two strongest issues (H1/H2, H3/H4) are corrected.

---

## Follow-up audit — 2026-10-16 (Priority 1–4)

This section records the outcome of the follow-up stabilisation pass. Commit
references are on `development/chatgpt-m6-m7`; nothing was pushed.

### Identifier mapping

The owner's request used a slightly different grouping. Per its instruction to
treat this document as authoritative, the identifiers are:

| This document | Owner's reference | Subject |
|---|---|---|
| M6 | M6 | Multipart parsed before CSRF/authorization |
| M7 | M7 | Restore accepts semantically invalid category graphs/specifications |
| M8 | M7/M8 or M9 | Category images excluded from asset validation |
| M9 | — | Multi-output preview labelled the wrong page (already fixed earlier) |

### Fixed in this pass

| Finding | Fix | Commit | Regression test |
|---|---|---|---|
| H6 | `persistSession` now requires an existing durable row with the same session id; only the reservation path may establish ownership | `fb88bcb` | `internal/wled/physical_locate_supersession_test.go` |
| M1 | Guarded atomic `CreateFirstUser` inside an immediate transaction; `CountUsers` errors surfaced | `76b52e7` | `internal/router/router_setup_test.go` |
| M6 | Multipart body bounded before parsing (CSRF middleware and part create/update) | `463997f` | `internal/middleware/csrf_multipart_test.go`, `internal/handler/parts_request_limit_test.go` |
| M7 | Manifest category/specification semantic validation before any restore insert | `2a23a17` | `internal/backup/restore_category_validation_test.go` |
| M8 | Category image references validated during candidate and active-generation checks | `2a23a17` | `internal/backup/restore_category_validation_test.go` |

**H6 — reproduction and fix.** A deterministic test was written first and
confirmed to fail against the pre-fix code (`stale activating snapshot
resurrected a superseded Locate session`). It drives a genuinely superseded
session (S1 stopped, replacement S2 completed on the same controller) and
replays the stale `activating` snapshot: pre-fix, `persistSession` re-created
the S1 durable row and re-established ownership; post-fix it is refused, no
obsolete WLED write is sent, and a direct command claim for the superseded
session id is rejected. The full `wled` package (including all existing
coordination, crash, recovery, timeout and multi-controller tests) passes under
both the normal and race-enabled suites.

### Unconfirmed hypotheses (no behaviour changed)

The owner asked to reproduce before changing behaviour. The following were
investigated; no deterministic reproduction was achievable without adding test
hooks, so behaviour was **not** changed. Each remains a recommendation.

1. **Backup snapshot skew.** `Export` (`internal/backup/service.go:119`) takes the
   database snapshot in one transaction, then walks the uploads directory outside
   it. Ordinary requests hold only the shared maintenance lock
   (`internal/maintenance/gate.go:108`) and `Export` acquires no gate lock, so a
   concurrent delete/add of an upload between the two steps can leave the archive
   inconsistent (manifest references a file that is absent, or vice versa).
   *Not reproduced:* requires interleaving between the snapshot and the walk, which
   needs an injected hook. *Recommended, low-risk:* have `Export` hold
   `gate.ReadLock()`, which excludes a concurrent restore (which swaps the uploads
   directory) without blocking ordinary requests. Full database/uploads
   consistency would require the exclusive lock (blocking writers) and owner
   sign-off.
2. **Category assignment/attribute race.** `SetProductCategory`
   (`internal/categories/service.go:523`) validates specifications, then opens a
   deferred transaction. A concurrent attribute edit could invalidate the earlier
   validation. *Not reproduced:* normal writes do not use immediate
   transactions, so serializing this needs both paths migrated to
   `BEGIN IMMEDIATE` — a broader change than this pass. Low blast radius
   (concurrent admin edits only).
3. **Image decode resource exhaustion.** `internal/images/processor.go` decodes
   before resizing with no pixel-dimension cap. *Not reproduced:* no decoder-level
   failure was demonstrated. *Recommended:* reject images above a pixel-count
   threshold before resizing.
4. **WLED protocol fidelity.** The loopback framebuffer fixture encodes the same
   segment/output reversal assumptions as production and does not model segment
   `frz` or a top-level runtime `ledmap`. `physicalState` decodes only `on`/`seg`.
   *Not reproduced:* requires real firmware. Remains an owner-verified
   commissioning item and a documented limitation (L2).

### Verification for this pass

`go build -tags fts5 ./...`, `go vet -tags fts5 ./...`,
`go test -tags fts5 -count=1 ./...`, `go test -race -tags fts5 -count=1 ./...`
and `npm test` all pass with the fixes and new regression tests in place.
Browser acceptance suites were not run (they require launching the full server
and a browser) and no hardware was contacted.

---

## High-severity findings

### H1 + H2 — Uploaded content was served unauthenticated and could execute script

**Files:** `internal/router/router.go` (upload route, pre-fix L38–39), `internal/documents/service.go` (save path).

**Evidence.** The uploads tree was registered as a bare file server *before* the read-authorization group:

```go
uploadsDir := http.Dir(config.DirUploads)
r.Handle(config.UrlPrefixUploads+"*", http.StripPrefix(config.UrlPrefixUploads, http.FileServer(uploadsDir)))
```

`internal/middleware/middleware.go` gates reads with `RequireReadAuth`, and `auth.User.CanRead` denies guests when `require_auth_for_read` is set — and the schema default is `require_auth_for_read BOOLEAN DEFAULT 1` (`sql/schema/001_init.sql:27`). The upload route bypassed all of that.

**Expected vs actual.** Expected: private installs do not expose uploaded files, and an uploaded HTML/SVG document never executes in the application origin. Actual: any client could fetch `/uploads/...` without a session, `http.FileServer` produced directory listings for index-less folders, and a part document named `report.html` was returned as `text/html` and rendered inline — script executing with the reader's session, able to read `meta[name=csrf-token]` and issue authenticated same-origin writes.

**Correction (Fixed).** New `internal/handler/uploads.go` (`HandleUploads`) replaces the bare file server and is registered inside `RequireReadAuth`. It refuses directories and directory listings, sets `X-Content-Type-Options: nosniff`, returns documents with `Content-Disposition: attachment` and `application/octet-stream`, and adds `Content-Security-Policy: default-src 'none'; sandbox` for images.

**Tests:** `internal/router/router_uploads_test.go` — guest access redirects to `/login` under default private reads; an authenticated admin receives `application/octet-stream` + `attachment`; a directory request returns 404.

### H3 — Same-second document uploads overwrote each other

**File:** `internal/documents/service.go` (pre-fix L95–98).

**Evidence.** Stored name was the sanitized basename plus a whole-second timestamp, written with a truncating `os.Create`:

```go
cleanName := fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
dst, err := os.Create(destPath)
```

**Expected vs actual.** Expected: each upload is a distinct file. Actual: two uploads named `datasheet.pdf` in the same second resolved to the same path; the second truncated the first while two `part_docs` rows still referenced it. Distinct original names that sanitize identically also collided.

**Correction (Fixed).** A 12-byte `crypto/rand` token is appended and the file is created with `os.O_CREATE|os.O_EXCL` (0o644), so collisions cannot silently overwrite. The original filename is still preserved as record metadata.

**Tests:** `internal/documents/service_collision_test.go` — two same-name uploads produce distinct paths with preserved bytes.

### H4 — Files removed before the database change committed

**Files:** `internal/parts/service.go` (`DeletePart`, `DeleteParts`), `internal/documents/service.go` (`DeleteDocument`).

**Evidence.** `DeletePart` and `DeleteParts` deleted image and document files *before* (or inside) the database delete; `DeleteDocument` deleted the file before deleting the row. `DeleteParts` ran `os.Remove` inside the transaction loop.

**Expected vs actual.** Expected: a failed delete leaves data intact. Actual (deterministic, no crash needed): `DeleteParts([validID, missingID])` removed the valid part's files, then failed on the missing id and rolled the transaction back — the row and its attachment references survived with no bytes on disk. Backup/restore asset validation then fails on the missing files. `DeletePart` also wrote its audit event before knowing deletion succeeded.

**Correction (Fixed).** Rows are deleted first; files are removed only after the transaction/statement commits, best-effort, via a shared `removeUploadedFile` helper. A failed delete now leaves orphaned files at worst (preferable to dangling references).

**Tests:** `internal/parts/service_delete_order_test.go` — a bulk delete with a trailing missing id rolls back and both the row and the file survive; cross-part link update is rejected and the whole update rolls back.

### H5 — Stock adjustment overflows int64 and clamps to zero

**File:** `internal/stock/service.go` (pre-fix `AdjustStock` L144–147).

**Evidence.** `newQty := assignment.Quantity + int64(req.Delta)` with no overflow check, followed by `if newQty < 0 { newQty = 0 }`. The schema constrains quantity only to be nonnegative (`sql/schema/001_init.sql`), so a stored quantity near `MaxInt64` is representable; `+1` wraps to a large negative value that is then clamped to **0** and committed.

**Expected vs actual.** Expected: reject an adjustment that would overflow. Actual: a valid large quantity is silently destroyed.

**Correction (Fixed).** `addQuantities` performs checked addition and returns `ErrInvalidStockRequest` on overflow; applied to `AdjustStock`, `AssignStock` merge and `MoveStock` merge.

**Tests:** `internal/stock/service_overflow_test.go` — `MaxInt64 + 1` is rejected and the stored quantity is unchanged.

### H6 — A stale Locate writer can resurrect a superseded session

**File:** `internal/wled/physical_locate.go` (attempt-marker persistence before the command claim; terminal-session replacement in `savePhysicalLocateSession`).

**Evidence (source-derived, not yet reproduced).** An activation persists its attempt marker before acquiring the command claim. `savePhysicalLocateSession` allows an incoming `activating` snapshot to replace a *different*, already-terminal session id. A process paused before its first persistence can, after a replacement session has started and stopped on the same controller, re-persist its old `activating` snapshot over the newer record and re-create that target's durable ownership — then pass the command claim and issue an obsolete activation.

**Expected vs actual.** Expected: only the dedicated reservation path creates/replaces session identities; a stale snapshot cannot re-own pixels. Actual: an obsolete writer can regain ownership after being superseded.

**Status: Open.** This is a concurrency/lifecycle defect with real-world (physical LED) impact, but the fix must not destabilise the existing safety model, and it needs a coordinating cross-process test (barrier immediately before the marker write). Recommended direction: require ordinary `persistSession` to match an existing session id and make ownership replacement exclusive to the reservation path, plus a monotonic per-controller generation/epoch fence checked inside the write transaction. Tracked as the highest-priority open item; **a targeted reproduction is required before any change is accepted.**

---

## Medium-severity findings

### M1 — Setup race can create multiple initial administrators

**File:** `internal/handler/auth.go:110–133`.

`CountUsers` is read, bcrypt hashing runs (slow), then `CreateUser` inserts unconditionally (`internal/db/users.sql.go`). Two concurrent `POST /setup` requests can both observe zero users before either inserts. **Open.** Recommended: perform first-admin creation atomically (single immediate transaction with a recheck, or an insert guarded by `(SELECT COUNT(*) FROM users)=0`), and fail closed on count errors (currently the error is discarded: `count, _ :=`).

### M2 — Empty/short passwords accepted at setup and forced reset

**File:** `internal/handler/auth.go` (setup L117–118, forced reset L182).

Both paths called `auth.HashPassword` directly without `auth.ValidatePassword`; bcrypt accepts an empty password. Note this is *not* exploitable as an authentication bypass, but it allows an empty-password administrator or clearing a forced-reset requirement with an empty password. **Fixed** — both paths now validate before hashing (400 on failure, no user created / no hash written). *Test:* `TestSetupRejectsWeakPassword`.

### M3 — `UpdatePartLink` was not constrained to the owning part

**Files:** `sql/queries/parts.sql` (`UpdatePartLink`), `internal/parts/service.go`.

The statement matched on link id alone, so an update targeting part A could rewrite part B's link. **Fixed** — the query now requires `id = ? AND part_id = ?` and uses `:execrows`; the service requires exactly one affected row. *Test:* `TestUpdatePartCannotRepointAnotherPartsLink`.

### M4 — Search returned the same part more than once

**File:** `sql/queries/parts.sql` (`SearchParts`).

`matched_parts(rowid, rank)` unioned `(rowid, rank)` pairs and the outer query grouped by `p.id, fts.rank`. A part matching both FTS (negative rank) and extended details (`0.0`) produced two output rows, consuming extra pagination slots. **Fixed** — the outer query groups by `p.id` only and orders by `MIN(fts.rank)`. *Test:* `internal/db/search_dedup_test.go`.

### M5 — Proposed wiring dropped reverse/serpentine/custom transforms

**File:** `internal/storage/cabinet_wiring.go` (`topologyWithProposedStrings`).

Proposed strings were rebuilt with empty `Direction`/`Pattern`, while loaded strings obtain these from their controller output (`internal/cabinetpixels/topology.go:317–325`, `outputPixelIndex`). Because empty pattern is treated as linear and empty direction as forward, an unchanged save of a reverse/serpentine output compared as forward/linear, so the mapping-impact check always demanded confirmation and could not detect real changes. **Fixed** — proposed strings inherit the current string's resolved transforms. *Test:* `TestTopologyWithProposedStringsPreservesOutputTransforms`.

### M6 — Wiring save could commit a topology that Load rejects

**Files:** `internal/storage/cabinet_wiring.go`, `internal/cabinetpixels/topology.go:317–320`.

An existing string linked to a controller output kept its `ControllerOutputID` through the save while its order/length could change; `Load` then rejects the mismatched pair, breaking cabinet layout, mapping preview and Locate. **Fixed** — the save now rejects changing the order or length of an output-linked string and directs the user to the controller-output settings. *Test:* `TestSaveCabinetWiringRejectsLinkedStringResize`.

### M7 — Multipart parsed before CSRF/authorization

**File:** `internal/middleware/csrf.go:109–124`.

When no `X-CSRF-Token` header is present, the middleware calls `r.ParseMultipartForm(1 << 20)` before validating the token. The `1 << 20` argument is a memory threshold, not a body limit, so a large unauthenticated/uncsrf'd multipart body is ingested/spooled before rejection. Route-level `limitRequestBody`/`MaxUploadSizeParts` run later. **Open.** Recommended: enforce a per-route `http.MaxBytesReader` before multipart parsing and reject before spooling. No confirmed RCE/data-loss; rated Medium (resource-exhaustion surface).

### M8 — Restore accepts semantically invalid category data

**File:** `internal/backup/service.go` (direct category/spec inserts), `internal/backup/restore_candidate.go`.

Restore bypasses `internal/categories/service.go` validation and inserts category/specification rows directly, so foreign-key and `quick_check` pass while semantic rules do not: acyclic parents, attribute↔category ownership, value type/range/options, required attributes. Normal service writes enforce these (`categories/service.go:476–520`). An out-of-category specification can also make a subsequent export fail (`backup/service.go:514–520`). **Open.** Recommended: run the shared domain validation over the candidate before activation.

### M9 — Category images excluded from restore/startup asset validation

**Files:** `internal/backup/restore_candidate.go:42–67`, `internal/backup/restore_generation.go:389–401`.

Asset validation covers parts and part documents only. Categories carry `image_path` (`sql/schema/021_categories_and_specifications.sql`), so a restored archive missing a category image keeps a dangling path that validation never detects. **Open.** Recommended: include category image references in pre-activation and active-generation checks with the same path/symlink/regular-file checks.

### M10 — Multi-output preview labelled the wrong page

**Files:** `internal/handler/hardware.go:151–155`, `web/static/js/controller_output_editor.js`.

The preview endpoint always returns page 0 for every non-selected output, but the client retained each output's page offset, so a non-selected output kept its `41–80 / 100` label while the server returned page-0 pixels beneath it. **Fixed (client).** After a successful refresh the component resets non-selected outputs' `preview_start` to 0 to match the server data. *Not covered by an automated test* (the Alpine factory needs a DOM harness); the change is small and local, and the existing JavaScript suite still passes.

### M11 — SIGTERM not handled

**File:** `cmd/server/main.go` (pre-fix L233–241).

`signal.Notify(c, os.Interrupt)` subscribed only to SIGINT, so the SIGTERM most supervisors and container runtimes send did not enter the graceful-shutdown path; the `Shutdown` error was discarded. **Fixed** — SIGTERM is handled alongside SIGINT and the shutdown result is logged. This is a lifecycle/hardening fix; it does not imply prior database corruption.

---

## Low-severity findings

### L1 — MCP corrupted non-ASCII search queries

**File:** `cmd/mcp-server/tools.go` (`urlEscape`).

The hand-rolled encoder emitted one code point per byte — `é` → `%E9` (should be `%C3%A9`), `中` → `%4E2D` — so the server decoded different text. **Fixed** — replaced with `url.QueryEscape`. *Not covered by an automated test*; the change is a one-line substitution of a standard library call.

### L2 — "Stopped" is not device-side fencing (limitation)

**Files:** `internal/wled/physical_locate.go`, `internal/wled/physical_locate_delayed_delivery_test.go`.

An acknowledged targeted cleanup clears durable ownership, but an activation already in flight at the controller can still relight the saved ranges afterwards. This is honestly encoded by an existing test (`physical_locate_delayed_delivery_test.go`). It is a physical-world limitation of HTTP-based control, not a code defect; it must be stated plainly in commissioning documentation and before enabling output.

---

## Hypotheses and unsupported configurations (not confirmed defects)

1. **Backup snapshot skew** — the database snapshot completes before uploads are archived (`internal/backup/service.go:128–157`), so concurrent attachment replacement could make an export inconsistent. No controlled reproduction was performed.
2. **Category validation/write race** — `SetProductCategory` validates before its transaction (`internal/categories/service.go:531–534`); concurrent attribute edits could invalidate that validation. Requires a coordinated multi-connection test.
3. **Image decode exhaustion** — suppliers'/users' images are decoded and resized without a visible pixel-dimension cap (`internal/images/processor.go:43–64,89–105`). Practical exploitability depends on decoder hardening and request limits.
4. **WLED protocol fidelity** — the loopback framebuffer interpreter encodes the same segment/output reversal assumptions as production and does not model segment `frz` or a top-level runtime `ledmap`; `physicalState` decodes only `on`/`seg`. The implemented custom-map checks therefore do not prove an active runtime map is absent. Real firmware/wiring remain owner-verified only.

---

## Missing test coverage (recommended follow-ups)

- Concurrent `POST /setup` (M1) and concurrent category-attribute edits (hypothesis 2).
- Malformed restore archives that violate category semantics (M8) and omit category images (M9); each must leave the database, uploads and generation unchanged.
- A coordinating multi-process test for the stale Locate session (H6), with a barrier before the attempt-marker write, asserting the old writer fails and no stale activation POST occurs.
- Multipart bodies with declared and chunked oversized lengths and invalid form tokens (M7), asserting bounded bytes read rather than eventual rejection.
- A DOM-level test for the controller-output preview paging (M10) and a unit test for the MCP encoder (L1).
- A DB-level wiring test that creates genuinely new (id 0) strings carrying custom patterns, plus reorder cases for every pattern and both directions.

## Architecture improvements (distinguished from confirmed defects)

These are structural observations, not defects, and were deliberately **not** implemented during a stabilisation pass:

1. **Centralise crash-safe file lifecycle.** Deletion is now commit-ordered but still best-effort. A trash/outbox table with a background reaper would give durable "delete row ⇒ eventually delete file" guarantees.
2. **Single password-setting path.** Setup, forced reset, self-service change and admin user creation should share one `SetPassword` that always validates.
3. **Explicit Locate generation fence.** A per-controller monotonic epoch checked inside the ownership write would make session resurrection structurally impossible rather than order-dependent.
4. **Share one domain-validation package** between the category service and restore, so backup/restore cannot establish state normal writes reject.
5. **Keep uploads outside the web-servable root** where practical; serving through the authenticated handler is the current mitigation.
6. **Bound request bodies before any middleware parses them**, including multipart, to remove the spooling surface.

## Operational limitations (not defects)

- Physical output remains opt-in and disabled by default (`WLEDGER_PHYSICAL_OUTPUT_ENABLED`); no real controller was contacted during this audit.
- Locate intentionally fails closed on RGBW, capacity mismatches, non-contiguous/duplicated output pins, matrix/custom-mapped controllers, inactive/whole-controller-inactive pins, segment gaps/overlaps, and non-solid/grouped/mirrored/offset target segments (`internal/wled/physical_locate.go`).
- Durable ownership serialises coordinating processes that share the one SQLite database; it cannot coordinate other databases, presets, external WLED clients or controller-side edits.
- Cabinet assignment is limited to the documented controller count; supported physical mapping is the documented linear/serpentine/custom-raster topology.

---

## Verification performed

Environment: Go toolchain at `/home/spetchal/gotoolchain/go` (go1.26.0), Node 22, `sqlc` present. All database/controller fixtures disposable; no live data or hardware.

| Check | Command | Result |
|---|---|---|
| Baseline full suite (pre-change) | `go test -tags fts5 -count=1 ./...` | PASS (all packages `ok`) |
| Build | `go build -tags fts5 ./...` | PASS |
| Vet | `go vet -tags fts5 ./...` | PASS |
| Full suite (post-change) | `go test -tags fts5 -count=1 ./...` | PASS |
| Race | `go test -race -tags fts5 -count=1 ./...` | PASS |
| JavaScript | `npm test` | PASS (20 grid-painter + 8 mapping patterns) |
| sqlc generation | `sqlc generate` | Clean regeneration; `internal/db/parts.sql.go` + `querier.go` updated |
| Whitespace | `git diff --check` | PASS |

Not run (and why): browser/Playwright acceptance suites (require launching the full server + browser), Docker Compose build, migration-upgrade-from-live-DB, and any real-hardware/WLED checks — the owner has not authorised hardware operations, and no live service was started.

## Physical LED commissioning checklist

Commission only after explicit owner authorisation, with output previously disabled:

1. Back up the installation database and uploads; confirm the deployed version and migration state (through `024_controller_output_configuration.sql`). Keep `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false`.
2. Confirm each controller's model/firmware, unique endpoint/port, reachability and timeout; confirm exclusive cabinet assignment and output order matches real wiring.
3. With read-only `/json/info` and `/json/cfg`, compare configured output counts/capacity, GPIOs, segment boundaries, direction/reversal, serpentine dimensions and any custom permutation against the physical strips; resolve mismatches first.
4. Verify cabinet path drawings independently of drawer/bin geometry; for every drawer enter each non-contiguous pixel range and compare resolved controller-local/segment-local ranges against physical LED positions.
5. Confirm inventory identities, quantities and QR labels are unchanged by hardware/mapping edits; rehearse a restore in a disposable copy.
6. Only then enable output in a maintenance window: test one controller and one drawer at low brightness; verify neighbouring/unowned pixels; exercise Stop and Global Off; test disconnect/reconnect and restart recovery; then re-disable output and confirm no active/partial ownership remains.
7. Record firmware/controller versions, tested mappings, observed payload/state, recovery results and any discrepancy before authorising routine use. Note the L2 limitation: a successful Stop means cleanup was acknowledged, not that an in-flight activation can no longer light pixels.
