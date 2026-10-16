# WLED LED operations, Test Centre and mapping

## Scope and safety

This implementation extends the existing cabinet/wiring architecture; it does not convert legacy LED coordinates or replace multiple-range drawer allocations. It is software-tested against disposable SQLite databases and loopback WLED simulators. **No physical hardware behaviour has been verified.** No deployment, production restart, production database restore, or physical controller test is authorized by this change.

The interface is available to editors and administrators at **Hardware → LED Test Centre** (`/hardware/led-test-centre`). Function settings are administrator-only in the existing **Settings → LED function settings** section. All new POST endpoints use existing authentication, role checks and CSRF protection.

Loading the Test Centre or selecting pixels reads saved configuration only. **Test Connection** explicitly performs read-only WLED discovery. Only explicit test actions start illumination. Existing recovery may send saved, verified range-off commands to finish an earlier operation; it never starts or resumes an illumination pattern merely because a page opens or the app restarts.

Physical output remains disabled by default through `WLEDGER_PHYSICAL_OUTPUT_ENABLED`. For a separate development installation containing **only loopback simulator controllers**, enable it explicitly to try simulated commands. Do not enable it against a production installation or physical controllers without separate authorization.

## Architecture retained and extended

- Legacy electrical containers/segments and `bins.led_index` keep their original meaning. The persisted `led_coordinate_space` flag still distinguishes segment-relative, drawer-relative and unresolved legacy data.
- Cabinets have ordered controller memberships, logical strings, physical wiring paths, and **1-based cabinet-wide pixels**. Physical layout/numbering and electrical mapping remain separate.
- A drawer owns one or more saved cabinet start/count ranges, potentially non-contiguous and spanning controllers. Reordering drawers does not infer a new wiring order.
- `internal/cabinetpixels` is the shared authority for cabinet/logical-to-controller physical addressing, including configured reverse/custom/serpentine mappings.
- `internal/wled` verifies WLED identity, capacity, real hardware outputs and safe segment geometry. WLED hardware outputs are not interchangeable with logical strings or segments.
- New bin overrides point to an existing allocation identity plus a **zero-based offset within that allocation**. The displayed drawer-relative number concatenates allocations in persisted display order. A stored physical destination/topology fingerprint prevents changed wiring from silently selecting a different LED.
- Live ownership uses the WLED-reported **MAC + electrical output pins + physical output-relative pixel interval**, not cabinet numbers or just database controller IDs. Aliased controller records therefore cannot own the same physical pixels independently.

### Index conventions

| Context | Convention |
| --- | --- |
| Test Centre absolute pixel entry | 0-based controller physical pixel |
| Selected hardware output-relative pixel | 0-based within the explicitly discovered output |
| Drawer/bin relative pixel | 0-based across the drawer's ordered saved ranges |
| Cabinet allocation start | 1-based cabinet-wide pixel |
| WLED JSON range stop | Exclusive, segment-relative API coordinate |

For example, a drawer owning cabinet pixels 21–40 can assign relative pixel 5 to cabinet pixel 26. The controller/output destination is derived from the actual cabinet topology, not assumed to be the same number.

## Ownership and Stop policy

1. Non-overlapping physical pixels may have simultaneous operations, including operations on the same controller.
2. Conflicts are rejected explicitly. A test or background legacy action cannot silently replace active Locate ownership. Locate priority is protective, not silent preemption: a conflicting Locate also requires explicitly stopping the conflicting operation first.
3. Legacy writes without exact durable ownership remain conservatively blocked while owned operations exist. They do not become a bypass around the new arbiter.
4. **Stop chosen operation** checks the authenticated owner and operation ID, cancels only that operation and sends only its recorded targeted range-off commands. The drawer page submits its displayed operation ID. Ambiguous drawer-only Stop requests are rejected.
5. Guided Previous/Next/Replay replaces only the pixel-identification operation created by that page: it first requests its targeted Stop, and will not start the replacement if cleanup fails. It does not stop a separate Locate.
6. Ownership, target snapshots and possible-delivery markers are committed before activation. SQLite writer claims and compare-and-set state transitions fence activation, cleanup, mapping changes and emergency cancellation across processes. Keep shared SQLite databases on filesystems supporting SQLite's locking contract.
7. Timeout schedules cleanup; it does **not** erase uncertain ownership. Failed cleanup remains visible/retryable. Disabling new output does not strand verified, persisted targeted cleanup.
8. Restart recovery cleans saved ranges. It does not replay a saved flash or sequence. Pattern frame failures remain visible after cleanup.

WLED's JSON API does not provide an exclusive lease against external applications. External presets, automations, realtime senders and manual WLED edits can still interfere. Use an operator-controlled commissioning baseline and network/access restrictions where needed.

## Original limitations and implementation corrections

The baseline already had controller CRUD, output configuration, cabinet mapping, verified Locate and crash recovery. The work retained those paths and addressed these gaps:

- Global single-session exclusivity prevented disjoint operations from coexisting, even on different physical pixels.
- Physical Locate used fixed brightness and the application lacked a complete central ten-function style model.
- No dedicated end-to-end Test Centre/guided discovery interface or independent physical-bin override existed.
- Concurrent drawers require session-ID-based Stop; selecting an arbitrary drawer session is no longer safe.
- Emergency delivery needed durable outcomes independent of existing sessions and of the requesting HTTP connection.
- Compact WLED MAC identities and colon/hyphen/dotted forms now normalize to the same ownership identity; formatting differences cannot create aliases or reject otherwise matching firmware identity.
- Integration review caught and corrected missing-settings compatibility, general-settings saves resetting hidden legacy colors, and sqlc regeneration overwriting manual extensions/model names.

The complete Go suite (including race detection), vet/build, JavaScript suite, isolated LED browser acceptance, existing CSRF/layout/physical-first/QR/scanner browser checks, and the extended fresh-install/restore/SIGKILL/multi-process acceptance passed during verification. CSS production generation passed with the existing optimizer warning about `@property --radialprogress`. The dependency install reported no npm vulnerabilities. Published-source independent review and physical commissioning are separate outstanding acceptance steps.

## Using the LED Test Centre

### Controller, output and pixel tests

1. Select the controller and click **Test Connection (read-only)**. Inspect capacity, firmware, hardware-output starts/lengths, pins and reversal.
2. Select the whole controller or an explicitly discovered hardware output.
3. Select a pixel in the bounded visual window or enter its absolute index. Selection alone sends nothing. The inspector shows cabinet/drawer/bin assignments, drawer-relative indexes and conflicts.
4. Choose color, brightness (0–255), duration (1–3600 seconds) and mode. Saved function styles can be copied into local test controls; those overrides do not change Settings.
5. Explicitly run an individual on/off/flash/replay, selected range, RGB channel, all-output or sequential test. Previous/Next-and-test are explicit command buttons. Server-side patterns hold ownership of their entire tested range, not just the currently lit pixel.
6. **Refresh saved data** shows acknowledged sequence progress, errors, active/unresolved ownership and persisted emergency results. Refresh does not start tests.
7. Use the prominent **Stop chosen operation**, or a row's **Stop / retry Stop**.

Partial-range operations do not power on a globally off controller, because that could wake unrelated LEDs. An explicit test owning the entire controller may wake global power after its durable reservation; it still requires safe, enabled solid segments. After Emergency Off, a full-controller test can re-enable global power. Unsupported/off/animated target segments are rejected rather than silently reconfigured.

Brightness scales requested RGB channels without changing controller-wide brightness or another operation's pixels. Existing WLED master/segment brightness, current limiting, gamma and wiring still affect actual optical brightness; the slider is not a calibrated physical light measurement.

### Drawer and cabinet tests

Select Cabinet and Drawer. The controller selection above does not restrict drawer tests: all controllers are resolved from saved ranges. The interface shows the saved ranges and allocated pixel count.

- Locate/all: illuminate every saved drawer pixel.
- First/last: test the first or last logical allocated pixel.
- Flash/sequence: operate on the drawer's allocated ranges with timeout.
- Mapped bins: step through explicit bin pixel assignments only; a drawer with no explicit bin assignments reports that clearly.
- Selected mapped bin: test its one assigned pixel.
- Cabinet: test only that cabinet's saved drawer allocations, not unassigned pixels on its controllers.

### Evidence is deliberately separate

- **Command requested**: the browser made an application request; this does not prove delivery.
- **Controller acknowledged**: WLED returned a validated JSON success acknowledgement, not merely HTTP 2xx. A timeout may mean a command arrived but its reply did not.
- **Physical behaviour observed**: a person's local checkbox/note, never automatically inferred by the application. This checkbox is not a persisted commissioning certificate.

Stop clears only owned ranges to black. It **does not recreate an unknown pre-test pixel framebuffer or external animation**: `/json/state` is not proof of all previous per-pixel colors. Use a known off/solid baseline for commissioning. Unrelated owned operations are preserved; the application does not guess how to restore external state.

## Guided mapping

### Find a physical pixel / mark drawer endpoints

1. Inspect the controller, optionally select an output, then open **discovery**.
2. Explicitly flash/replay pixels or use Previous/Next-and-test while observing the installation.
3. Select the cabinet/drawer, mark the first and last observed physical pixels, then **Preview drawer mapping**. Changing the selection invalidates marked context.
4. Review the proposed cabinet coordinates, adjust ranges if needed and resolve reported conflicts.
5. Stop outstanding operations, confirm the append and save. The server revalidates capacity, drawer/cabinet relationships and overlap **inside the save transaction**. Existing ranges are preserved; this wizard appends rather than silently replacing them.

Hardware output selection is a test boundary, not permission to reinterpret a WLED output-array index as a logical string order. Discovery inputs remain controller-absolute zero-based indexes, including on nonzero outputs. The canonical topology performs reverse/custom wiring conversion.

### Allocate several drawers

In **allocation**, select drawers in the intended allocation order, enter a **1-based cabinet start** and count per drawer, and preview. Adjust individual proposed starts/counts before confirming. All proposals save atomically or none save. Capacity and overlap checks are repeated on commit; no partial allocation is retained if a later proposal fails.

### Assign or correct a bin pixel

Select a bin physically attached to the selected drawer and a controller pixel inside that drawer's saved ranges, then **Save selected pixel to bin…** and confirm. The backend finds the unique allocation and relative offset; duplicate/out-of-range assignments are rejected. Inventory identities, quantities and legacy electrical indexes are untouched.

**Clear selected bin pixel override…** explicitly removes only that bin's override, including a stale override. It preserves drawer ranges and returns Locate to the existing whole-drawer fallback for that bin. Clear/reassign affected overrides before replacing or deleting a referenced allocation: database guards prevent accidental retargeting. A topology/attachment change invalidates the old override instead of silently converting it.

## Validation and troubleshooting

The **validation** tab reports saved-data errors and warnings with controller/cabinet/drawer/bin references and suggested repair. It includes inconsistent references, invalid capacities/permutations, out-of-range allocations, overlap/known aliases, missing drawer/bin assignments and stale bin overrides. Controller records also carry saved configured/unassigned capacity counts.

Static validation cannot discover unknown DNS aliases or prove a physical output exists without explicit read-only inspection. Runtime reservation performs the authoritative live MAC/output conflict check before activation. No report automatically reassigns pixels.

| Symptom | Check |
| --- | --- |
| Physical output disabled | This is the default safety gate. Use a simulator-only dev installation or obtain physical commissioning approval. |
| Mapping unverified/stale | Review actual controller count/output configuration and explicitly verify it through the existing controller mapping screen. |
| Controller off | Partial tests cannot wake unrelated LEDs. Use an explicit whole-controller test or operator-controlled WLED baseline. |
| Conflict/owned pixels | Inspect operation IDs and stop only the intended operation. Do not erase durable records to unblock a device. |
| Timeout/offline/ack failure | Delivery may be uncertain. Review retained targets, restore communication, and retry targeted Stop. Other controllers remain independently reportable. |
| Stale bin override | Review the changed attachment/wiring, then explicitly clear or reassign the override. |
| Allocation edit refused | Referenced bin overrides protect allocation identity. Clear/reassign them first. |
| Optical brightness unexpected | Check WLED master/segment brightness, power/current limits, channel wiring and actual hardware. |

## Emergency Global Off

Use **Emergency global off…** and type **EMERGENCY OFF**. This is deliberately separate from selected-operation Stop.

Acceptance durably cancels activation authority before delivery. The service then attempts Off on every currently configured controller with independent bounded deliveries and durable outcomes—even if the requesting browser disconnects or an earlier controller fails. Earlier acknowledgements cannot be rolled back by a later network failure.

Results separately report endpoint, attempted, acknowledged and error. Failed/unrecorded outcomes retain the global emergency barrier; refresh/restart does not discard them. Fix communication and explicitly retry Emergency Off. Saved historical endpoints no longer configured for that controller are reported as **unattempted/unresolved**, not blindly contacted: an old IP might now belong to another device. These require separately authorized, identity-safe reconciliation. Do not bypass the barrier by deleting database rows.

Emergency Off deliberately affects all configured controllers, including external/unowned illumination on them. It does not claim to switch off an unreachable controller physically.

## Persistence, backup and migration

- `025_physical_pixel_ownership.sql`: lossless session-keyed ownership migration, physical claims and expiry/function metadata. It preserves existing target snapshots. Unidentified legacy active operations block new concurrent ownership until cleanup.
- `026_physical_bin_pixels.sql`: additive physical bin overrides and allocation guards. Existing bins are not automatically assigned. Rollback refuses to discard overrides.
- `027_physical_emergency_outcomes.sql`: durable per-endpoint emergency results and generation fencing, independent of configuration/session deletion. Rollback refuses to discard emergency evidence.
- Existing backup envelope 2.3 exports physical bin overrides explicitly plus all system flags, including function styles. Current readers retain older supported archives; restoring an older archive that omits overrides is rejected if it would discard existing overrides. Older applications whose schema/backup reader does not understand these additions must not be used to restore the new export.
- Runtime ownership/emergency records remain installation-local safety evidence, not instructions to replay illumination from a backup. Restore remains blocked while durable ownership or emergency recovery is unresolved.
- Do not edit already-applied migrations or force a rollback. Export before upgrading. Down migrations are guarded safety operations, not an automatic escape from new data.
- Manual LED query extensions live in `internal/db/led_querier.go`, outside generated sqlc contracts. `sqlc generate` is verified to retain a compiling application.

## Automated verification

All controller command tests use mocks/simulators. The simulator is documented in [WLED_SIMULATOR.md](WLED_SIMULATOR.md).

```sh
go test -tags fts5 -count=1 ./...
go test -race -tags fts5 -count=1 ./...
go vet -tags fts5 ./...
go build -tags fts5 ./...
npm test
npm run test:led-browser
npm run test:csrf-browser
npm run test:layout-browser
npm run test:task067-locate
```

Browser tests require Playwright Chromium, Go, Python and (for the existing physical-first QR test) Python OpenCV. The LED browser test uses Alpine's pinned development dependency to avoid CDN requirements, creates its own database/server/controller, and rejects external app targets. Its controller/mapping/settings flows and emergency success exercise real local APIs; intentionally inconsistent map rendering and partial-emergency HTTP 502 rendering use explicitly labelled stubs. Backend tests separately exercise actual emergency network failures, cancellation, persistence failures and restart recovery.

## Remaining physical/compatibility limits

Real GPIO wiring, strip order, RGB channel order, current delivery, firmware differences, optical effects and external writers require separately authorized human commissioning. Supported safe WLED addressing remains RGB with explicit hardware outputs and a partition of supported solid segments; unsupported RGBW, matrices, skips/remapping/grouping/effects are rejected, not guessed. The existing cabinet membership editor/schema retains its five-controller-per-cabinet limit; this change does not hardcode drawer/bin counts or replace the cabinet architecture. Sequences are 500 ms steps with a bounded duration, not calibrated timing tools. Unknown previous per-pixel state is not reconstructed.
