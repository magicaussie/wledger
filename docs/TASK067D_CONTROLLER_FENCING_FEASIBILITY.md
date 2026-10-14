# Task 067D — WLED Controller Capability and Fencing Feasibility

**Repository:** `magicaussie/wledger`
**Branch / baseline:** `development/chatgpt-m6-m7` at `72bb6ec83aa77aa163249a08309da3c427c756bd`
**Remote baseline:** `origin/development/chatgpt-m6-m7` resolved to the same SHA at investigation start.
**Verdict:** **PARTIAL — source-level feasibility supports a core-integrated design, but no WLED firmware build or controller validation was possible. Stock WLED does not provide the guarantee.**
**Production Locate/Stop integration:** none made.
**Physical WLED:** not contacted; testing remains blocked.

## 1. Executive summary

A standalone usermod is **not sufficient** to guarantee that a stale Locate command cannot change protected output. WLED v16.0.1 exposes a custom usermod lifecycle, a shared web server, and a pre-output overlay callback, but its standard HTTP JSON and WebSocket paths call `deserializeState()` directly. That function mutates global and segment state, writes individual pixels into segment buffers, schedules rendering, and then calls usermod notification hooks. The usermod API has no pre-mutation veto callback covering those paths, nor a controller-wide command queue, durable applied-command receipt, or per-bus completion receipt.

The technically justified enforcement boundary is a **WLED core-owned serialized command-application path**: validate the command envelope and generation immediately before any protected state/pixel mutation; make Stop advance a durable generation floor and close that generation before targeted black is applied; serialize this with render/effect work; then acknowledge only after a correlated output completion barrier for every affected bus. Commands received before Stop but processed after it must re-check the generation at that point and be rejected. A response sent after JSON parsing or logical mutation is too early.

A host-only C++ protocol state-machine prototype was created and passed deterministic host tests. It is **not a WLED firmware patch, simulator of WLED internals, firmware build, or evidence of actual WLED behavior**. The full firmware build was not attempted: PlatformIO is not installed, the available `arduino-cli` has no standard ESP32 core installed, and the target hardware/platform is unknown. No toolchain was installed and no firmware was flashed.

**Decision:** stock WLED and a standalone usermod are insufficient. A maintained, pinned WLED fork (with a usermod for the custom protocol plus core hooks across all write paths and output completion) is the leading candidate. The current external-writer policy is unenforced, so the overall architecture remains incomplete. Keep WLEDger production Locate/Stop unchanged and retain unresolved ownership on stock controllers after uncertain delivery.

## 2. Baseline, protection, and evidence boundaries

- `HEAD` and the authorized remote branch tip both matched `72bb6ec83aa77aa163249a08309da3c427c756bd`.
- Read `AGENTS.md`, Task 067B, Task 067C, Task 067’s fault acceptance report, `scripts/task067c_command_order_model.py`, current `PhysicalLocateService` Locate/Stop/recovery code, the Task 067B delayed-delivery test, hardware documentation, and controller example/test fixtures.
- Pre-existing user-owned changes were preserved: modified `docs/OMO_HANDOVER.md`, untracked `.task055b-staging/`, and untracked `docs/TASK056_FUNCTIONAL_AUDIT.md`. None was edited, staged, or used. The staging directory includes database/WAL files, an `operator.secret`, and runtime artifacts; it remains excluded from this task.
- The only new tracked-intent paths are this report and `experiment/task067d-controller-fencing-poc/`.
- No production, staging, live controller, database, or physical hardware endpoint was contacted.

Evidence labels below distinguish pinned-source review, existing loopback WLEDger tests, and this new host model. A source review or host model is not real-firmware evidence.

### Current WLEDger physical Locate/Stop path reviewed

`PhysicalLocateService.locate()` resolves and fingerprints controller output mappings, persists an `activating` session with immutable per-controller cleanup targets, and then persists each target's `MayHaveReceived` marker before sending its ordinary WLED `/json/state` activation. Stop/recovery rechecks the saved endpoint and output fingerprint, persists `stopping`, sends targeted black state writes for the saved commands, and clears each target marker only after the HTTP client call returns successfully and the resolution is durably stored; the session then becomes `stopped`. SQLite claims serialize WLEDger database operations, but the controller call is still ordinary stock JSON and the client acknowledgement is not a correlated fence, render barrier, or LED-bus completion receipt. Task 067B's deterministic loopback test proves that its mock can apply the earlier activation after cleanup acknowledgement and durable release. This is the reason Task 067D does not alter the production Locate/Stop implementation.

## 3. Known and unknown controller hardware

### What the repository establishes

- `hardware/README.md` describes an optional chainable 8-LED PCB design for 5 V SK6812-family LEDs, with eight boards forming a 64-LED segment. This is LED-board design evidence, **not evidence identifying an installed WLED controller**.
- `examples/hardware_config_example.json` and `examples/hardware_config_drawer_cabinet.json` contain illustrative controller addresses, segment IDs, counts, and grid/linear examples. They are sample configuration, not deployed inventory.
- Locate test fixtures use in-memory loopback controllers, RGB framebuffers, synthetic output counts/segment IDs, and deterministic HTTP barriers. The sample IPs and counts in examples/tests do not identify physical hardware.
- The WLEDger model records an IP/port, LED capacity, output configuration and mapping fingerprint, but the inspected records do not prove the controller’s physical make/model, installed firmware build, bus driver or deployment state.

### Owner checklist — complete before choosing a firmware target

For each intended controller, record and independently verify:

- [ ] Manufacturer, exact board/model and hardware revision.
- [ ] SoC/platform (ESP32 variant, ESP8266, or other) and chip revision.
- [ ] Flash capacity, partition table, available RAM, and PSRAM capacity/type.
- [ ] Installed WLED release, exact firmware build/commit and build configuration.
- [ ] LED chip/protocol, bus driver/method, number of outputs, output IDs/pins and bus lengths.
- [ ] Segment IDs/bounds, LED maps, physical pixel order and output-to-range mapping.
- [ ] Network transport and enabled interfaces: HTTP, WebSocket, MQTT, UDP sync, E1.31/Art-Net/DDP, serial/Adalight, ESP-NOW, or Ethernet.
- [ ] OTA/rollback state, serial recovery availability and a verified recovery image/procedure.
- [ ] Enabled WLED usermods, buttons, IR, power-on preset, playlists and other WLED clients.
- [ ] Whether another application, bridge, controller, or person can write to the same LED bus/ranges.
- [ ] Stable controller identity/provisioning method and whether a factory reset or firmware rollback can erase or restore it.

No controller specifications are inferred from the PCB, examples, test fixtures, MAC/IP fields, or WLEDger mapping metadata.

## 4. Pinned WLED source baseline and build feasibility

### Source revision

Task 067C’s initial upstream reference was verified against the GitHub tag reference:

- WLED tag: [`v16.0.1`](https://github.com/wled/WLED/tree/v16.0.1)
- Tag object: commit `29b389df1c1aaec6ff53aea742d17063b985906c` (annotated/ref resolution returned this exact commit).
- All source claims in this report refer to that commit, not a moving branch. The local Task 067D host prototype does not compile or vendor WLED sources.

### Relevant source paths and behavior

| Source path / function | Observed behavior relevant to fencing |
|---|---|
| [`wled00/wled_server.cpp` — `initServer()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/wled_server.cpp) | Registers the standard `/json` async JSON handler. After obtaining/parsing the shared JSON document it calls `deserializeState(root)` for ordinary state writes, releases the JSON lock, then responds with `{"success":true}` or verbose state. That response is not a controller generation receipt or bus-drain guarantee. |
| [`wled00/ws.cpp` — `wsEvent()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/ws.cpp) | Complete text frames are parsed and passed to `deserializeState(root)` inline; the success response has no client command ID, epoch or generation. Binary E1.31/Art-Net/DDP payloads dispatch to realtime handlers. |
| [`wled00/json.cpp` — `deserializeState()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/json.cpp) | Mutates global brightness/on/transition/realtime settings, suspends the strip around segment changes, calls `deserializeSegment()`, invokes `UsermodManager::readFromJsonState(root)`, schedules presets/playlists, then updates state. The usermod JSON callback runs after standard state/segment processing has begun; it is not a before-mutation authorization hook. |
| [`wled00/json.cpp` — `deserializeSegment()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/json.cpp) | Applies segment geometry, on/off, colors/effects/transitions and `seg[].i` individual pixel writes. Individual LED values are written to the segment raw-pixel buffer and `strip.trigger()` requests rendering. A custom endpoint does not gate this path. |
| [`wled00/wled.cpp` — `WLED::setup()` / `beginStrip()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/wled.cpp) | Initializes the strip and applies a configured boot preset before `UsermodManager::setup()` and before `initServer()`. A fail-closed fence must be loaded/advanced and output writes gated before boot-preset or boot-state output can reach LEDs; ordinary late usermod setup is too late. |
| [`wled00/wled.cpp` — `WLED::loop()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/wled.cpp) | Runs connection and input handlers, usermod loops, transitions, nightlight, Hue, playlists, presets and `strip.service()`. There are internal writers in addition to external JSON requests. |
| [`wled00/presets.cpp` — `applyPreset()` / `handlePresets()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/presets.cpp) | Preset selection queues work for the loop; `handlePresets()` later loads preset data and invokes `deserializeState()`. Treat the delayed internal application as a writer that must use the fence path. |
| [`wled00/FX_fcn.cpp` — `WS2812FX::service()` / `show()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/FX_fcn.cpp) | Effects/transitions execute in `service()`, compose the output frame, invoke the pre-show callback, then paint mapped pixels and call `BusManager::show()`. Source explicitly says some buses send asynchronously and `show()` may return before all data is sent. |
| [`wled00/bus_wrapper.h` — `PolyBus::show()` / `canShow()`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/bus_wrapper.h) | Supports multiple bus/driver implementations with per-driver `Show()` and `CanShow()` methods. This is evidence of driver diversity, not proof that every selected driver exposes a correlated completion receipt suitable for safe release. |
| [`wled00/um_manager.cpp` / `usermods/EXAMPLE/usermod_v2_example.cpp`](https://github.com/wled/WLED/blob/29b389df1c1aaec6ff53aea742d17063b985906c/wled00/um_manager.cpp) | Usermods have setup/loop/connected, JSON config/state/info, button, MQTT, UDP, overlay, and state-change hooks. No registered pre-state-mutation fence hook is present in the inspected interface. |

### Build configuration and why no firmware build was run

The WLED v16.0.1 `platformio.ini` selects a set of board environments and pins important libraries, including NeoPixelBus commit `1d7ff38f14d04a976f9c6e365c83a232bcba04fc`, Aircoookie ESPAsyncWebServer `v2.4.2`, ESP32 AsyncTCP `3.4.7`, and IRremoteESP8266 `2.8.2`. Its ESP32 default uses a Tasmota PlatformIO platform archive labelled `2024.06.00` (Arduino-ESP32 2.0.18 / ESP-IDF 4.4.8); the ESP8266 default uses `espressif8266@4.2.1`. The configuration also references a mutable QuickESPNow branch, so a reproducible build would need to pin that resolved commit as well as all platform packages. These are upstream build inputs, **not** the dependencies of the host prototype.

Environment check on this machine: `g++ 13.3.0` and `arduino-cli 1.5.2-rc.1` are available; PlatformIO/`pio` is absent. `arduino-cli core list` shows only `esp32-bluepad32:esp32`, not the standard ESP32 or ESP8266 WLED platforms. The ESP32 cross-compiler alone is not a WLED toolchain: the pinned framework, platform packages, source checkout, release/override config and library graph are missing. No system packages were installed and no host configuration was changed. A full WLED firmware build is therefore **NOT RUN / not reproducible in the current environment without provisioning a pinned isolated build environment**.

## 5. Usermod capability matrix

Status is for WLED v16.0.1 source/API as inspected, not all WLED versions.

| Capability | Status | Evidence and qualification |
|---|---|---|
| Register a custom command endpoint | **SUPPORTED (with qualification)** | `wled.h` exposes the shared `AsyncWebServer server`; `WLED::setup()` calls usermod setup before `initServer()`. A usermod can register `server.on(...)` directly. This is not a dedicated stable usermod route-registration abstraction; handler priority/collision/version compatibility is **UNVERIFIED**. |
| Validate generation/sequence on the custom endpoint | **SUPPORTED for that endpoint only** | A usermod can implement its own parser and state machine. It cannot make ordinary WLED state changes use the same validation. |
| Prevent obsolete commands from changing protected pixel/segment state | **REQUIRES CORE PATCH** | No pre-mutation callback surrounds `deserializeState()` / `deserializeSegment()`; usermod read callbacks are downstream of core parsing/mutation. A `handleOverlayDraw()` mask is later at render output and does not prevent state mutation or delayed effect/preset changes. |
| Intercept standard HTTP JSON state writes | **REQUIRES CORE PATCH** | `initServer()` calls `deserializeState()` before returning the standard response. A usermod has no pre-parse/pre-apply veto on this path. |
| Intercept standard WebSocket state writes | **REQUIRES CORE PATCH** | `wsEvent()` calls `deserializeState()` directly. One WS connection or its success response does not add fencing. |
| Intercept presets, playlists, effects and transitions | **REQUIRES CORE PATCH** | Presets apply through delayed main-loop `handlePresets()`/`deserializeState()`; effects and transitions mutate/render from `strip.service()`. Usermod state-change notifications are not a universal apply gate. |
| Intercept all realtime LED input protocols | **REQUIRES CORE PATCH** | Usermod has an `onUdpPacket()` callback, but the source has distinct E1.31/Art-Net/DDP, UDP/realtime, serial/Adalight and other paths. The usermod callback is not a universal guard over every route to `strip`/bus state. |
| Enforce total order across network connections and internal writers | **REQUIRES CORE PATCH** | A custom handler can serialize its own callers only. Async HTTP/WS, main-loop presets/effects, realtime inputs and other clients do not share a documented usermod-owned command queue. |
| Observe a correlated LED bus transfer completion | **REQUIRES CORE PATCH** | A usermod can poll broad `strip.isUpdating()` availability, but there is no per-command/per-output completion token. `show()` may return while bus output remains in flight. Exact driver completion capability remains **UNVERIFIED** by platform. |
| Persist minimum fencing state safely | **UNVERIFIED** as a usermod-only capability | `addToConfig()`/`readFromConfig()` can use WLED's filesystem-backed config lifecycle; that lifecycle is for settings, not a transactional fence journal. A per-platform NVS/journal implementation is technically possible, but atomicity, recovery, mount failure, wear and reset behavior are unproven. |

A custom endpoint plus overlay does **not** close the gap: ordinary API, WS, presets, effects, realtime protocols and local inputs can still mutate state or the bus; normal boot preset application occurs before usermod setup. A final-output overlay may be a useful extra mask, but not the sole safety mechanism.

## 6. Enforcement-point and ordering analysis

Required command path:

`network receive → parse/envelope validation → shared serialized apply executor → durable epoch/generation check → protected-state mutation → render-loop frame → per-output bus transfer → correlated acknowledgement`

The generation/sequence comparison belongs in the **shared core command-application executor immediately before any protected pixel or segment state changes**, not merely in a custom URL handler or a response callback. Every API and non-API state writer must be routed through this executor or rejected while a fenced range is owned.

Required Stop ordering for each controller:

1. WLEDger already has durable ownership, exact controller identity, output fingerprint, ranges and possibly-sent command records.
2. Controller validates identity/protocol/output/range bounds and accepts a strictly newer Stop generation.
3. Controller atomically persists the new generation floor with the generation closed for further activation before acknowledging fence acceptance. If persistence fails or is inconsistent, it fails closed and sends no successful receipt.
4. The single executor waits for any currently running protected mutation to finish, invalidates queued lower-generation work at apply time, and schedules exact black writes for the saved ranges. It must not apply controller-wide off.
5. The render path prevents effects/transitions or any writer from replacing the owned range. If a prior bus transfer is still active, the new black frame is sent after that transfer completes; the final response waits asynchronously for completion of all affected buses. Do not block an async network task or watchdog with an unbounded wait.
6. The acknowledgement identifies installation, controller, boot epoch, generation, sequence, command ID, output fingerprint, result, and completion level. WLEDger retains ownership on a lost/invalid receipt and persists each controller receipt before session release.

For a command already executing when Stop arrives, the core lock/queue must serialize it before Stop cleanup. For a command merely received or queued but not yet applied, generation validation happens when it reaches the apply boundary, so it is rejected after the Stop floor is persisted. A generic HTTP response after `deserializeState()`, a successful GET, or a disconnect does not prove render/bus completion.

Output bounds must be validated against physical output identity and layout; segment-local coordinates alone are insufficient after bus/segment/LED-map reconfiguration. A static/provisioned owned-range allow-list or equivalent WLEDger-signed capability is needed so the fenced endpoint cannot accidentally broaden its writes. No protocol authenticity is implemented in the host prototype; plain stock HTTP is not an authentication guarantee.

## 7. Persistence and reboot strategy

### State required

At minimum, persist a versioned, checksummed record containing:

- Stable installation and controller identity (not only mutable IP/name).
- Fencing protocol version and compatible firmware/build capability identity.
- Monotonic boot epoch.
- Accepted generation floor and whether the current generation is closed by Stop.
- Output-layout fingerprint / output identity binding.
- Any provisioned owned-range capability required by the controller.

Sequence counters and a bounded command-ID/result cache may remain RAM-resident **only if** every boot durably advances the epoch before any command/preset/output processing. Old-epoch retries then fail. If the epoch cannot be advanced/read, no fenced command is accepted. WLEDger remains the source of immutable cleanup ranges and durable per-target progress; after reboot it re-handshakes the current epoch and sends a new higher-generation recovery Stop while keeping database ownership unresolved.

### Storage options and risks

| Option | Feasibility / risk |
|---|---|
| ESP32 NVS/Preferences | Candidate for initial ESP32-only implementation; not tested here. Use redundant/sequence-numbered records with integrity checking and an atomic valid-record selection strategy. Prove commit semantics under power cuts for the exact Arduino/IDF/core and flash module. Persist boot epoch and generation/closed-floor transitions, not each pixel or every sequence. |
| WLED config filesystem (`cfg.json`/LittleFS) | Not recommended as the fence journal. Usermod config is saved through normal settings/config operations, not synchronously as a safety barrier. WLED setup uses filesystem mount/recovery behavior; the inspected ESP32 path calls `WLED_FS.begin(true)`, so a filesystem failure may trigger format-on-failure. Factory reset/config restore can also replace configuration. |
| Separate filesystem journal / flash slots | Possible only with a designed two-slot or append-only journal, checksums, sequence selection and write verification. Wear, filesystem recovery and torn-write behavior need target testing. |
| RTC memory | Not power-loss durable and not suitable for the required epoch/floor guarantee. |
| ESP8266 emulated EEPROM/flash journal | Platform-specific and unverified; not portable with ESP32 NVS. A portable multi-platform guarantee would require separate reviewed/tested backends or dropping ESP8266 support. |

A write/commit failure, torn record, impossible counter, identity mismatch, empty/corrupt store, output mismatch, factory reset, erased NVS, or firmware without the fencing capability must leave the controller in a fenced-unavailable state. `boot_epoch` and `generation` never wrap; the controller requires a deliberate re-enrollment/recovery path at exhaustion. Firmware downgrade/rollback must be detected before output becomes controllable; ordinary version strings are not secure anti-rollback evidence. Controller replacement must not inherit an unresolved controller identity merely because IP/port are reused.

A bounded flash-write policy is feasible in principle: one durable write per boot and writes when an activation advances the accepted generation and when Stop closes/advances it; no per-pixel writes. Exact write frequency depends on command grouping and fence protocol. Flash endurance cannot be quantified without the installed flash part, selected storage backend, actual Locate duty cycle and power-failure profile.

## 8. External-writer policy

**Proposed policy: A — exclusive fenced-range ownership.** While WLEDger owns a range, every state writer capable of changing that range must be rejected or safely isolated at the same core enforcement boundary. Preserve other ranges where possible. Global brightness/power changes, segment-bound changes, LED-map changes, and effects that can overlap owned pixels are conflicts and may need to be rejected controller-wide while any range is owned. Do not silently disable ordinary WLED features in production.

| Writer | Can change protected pixels? | Required enforcement while owned |
|---|---|---|
| Web UI / standard HTTP JSON | Yes | Core gate before `deserializeState()` mutation; reject writes that overlap or globally affect owned output. |
| WebSocket | Yes | Same core gate as HTTP; no bypass for a second WS client. |
| Presets / playlists / boot preset | Yes | Gate internal preset application too. Load/reconcile durable fence before boot preset or any output. |
| Effects / transitions / nightlight | Yes | Prevent render/effect code from painting fenced ranges; cancel/contain transitions and pending frames on ownership acquisition/Stop. |
| MQTT / Hue / Alexa integrations | Potentially | Route state changes through same gate; do not rely on WLEDger database locks. |
| WLED UDP sync / notifications | Potentially | Gate state application at the consumer, independent of network endpoint. |
| E1.31 / sACN, Art-Net, DDP and WebSocket binary realtime | Yes, direct realtime path | Gate at realtime pixel ingestion and output application; custom JSON endpoint does not cover these. |
| Serial / Adalight / TPM2 / ESP-NOW | Potentially | Gate or disable in the build/ownership mode; enumerate actual feature flags. |
| Buttons / IR / power-on settings | Yes | Gate internal state changes; usermod button hook alone does not cover every input path. |
| Other usermods | Yes; can call strip APIs directly | Only audited allow-listed usermods, or provide a core ownership API they must use; arbitrary usermods cannot be trusted to cooperate. |
| Another WLEDger/external process | Yes | Authenticated protocol plus device-side gate; network ACL is defense-in-depth only. |

Policy A is **not implemented or demonstrated**. If any source cannot be routed through the fence or output isolation cannot be proven, the system must report fencing unavailable rather than declare safe release. Policy C (exclusive controller control) is a possible fallback only if firmware and network policy can actually disable every other writer; an external gateway by itself is not that control. Policy B would require a larger explicit priority/restore protocol and is not recommended for the first implementation.

## 9. Isolated prototype design and results

Prototype files:

- `experiment/task067d-controller-fencing-poc/fence_state_machine.hpp`
- `experiment/task067d-controller-fencing-poc/fence_state_machine_test.cpp`
- `experiment/task067d-controller-fencing-poc/README.md`

The host model checks protocol version, controller/installation identity, output fingerprint, a provisioned owned-range allow-list, output bounds, monotonic generation/sequence, exact duplicate command IDs, closed Stop generations, persistent epoch advance, a post-boot recovery-Stop gate, and targeted range writes. After every boot it rejects activations until a current-epoch targeted Stop is applied. It commits a new generation/closed fence before mock pixel writes and fails closed on an injected journal-commit failure. The implementation also makes an output-write failure leave the model not-ready, but that branch is not injected by the current test. Acknowledgement identity is checked in the test.

The test journal is in-memory; its atomic replacement contract is an interface assumption. It is not NVS/filesystem code. Its 32-bit FNV-1a record checksum detects simple accidental changes in the model but is neither a cryptographic authenticator nor an implementation of WLED flash integrity. The pixel output is a synchronous vector framebuffer, not WLED `Segment`, `WS2812FX`, bus-manager or physical output. The test does not use network sockets or reproduce WLED’s execution/task scheduling. This is an **isolated host protocol model**, not a firmware-side PoC.

Host build/test command and results are in §13. The existing Task 067C Python reference model passed its 17 assertions. The existing Task 067B delayed-activation Go test also passed **as a failure reproducer**: that pass means it reproduced the unsafe mock ordering, not that stale activation was prevented.

### Prototype acceptance boundary

| Property | Host prototype | Pinned WLED build | Real controller |
|---|---|---|---|
| Generation/sequence/replay rules | Simulated and host-tested | Not built/integrated | Not tested |
| Durable boot epoch/floor | In-memory atomic-journal test double only | Not tested | Not tested |
| Segment/output mapping and range limits | Simplified fixed mock output and allow-list | Not tested against WLED buses/segments/maps | Not tested |
| Network path / delayed receive | Not present | Not tested | Not tested |
| Render/effect/preset serialization | Not present | Not tested | Not tested |
| Bus output completion | Synchronous mock write only | Not tested | Not tested |
| External writer exclusion | Not implemented | Not tested | Not tested |

## 10. Deterministic failure matrix

“Model PASS” below means only the host C++ state-machine test or Task 067C reference model behaved as specified. Current WLEDger’s loopback reproducer can pass as a test while demonstrating a safety failure. No row is real-WLED or physical-hardware acceptance.

| ID | Expected behavior | Observed evidence / result | Stale activation rejected? | Safe to release ownership? | Remaining uncertainty |
|---|---|---|---|---|---|
| F01 delayed activation after acknowledged Stop | Higher durable floor rejects the old command at apply; Stop receipt follows targeted black/drain. | Task 067B Go loopback mock **reproduces relight** after WLEDger is stopped; host prototype **Model PASS** rejects old generation after Stop. | Mock: **No**. Host model: **Yes**. | Stock WLED: **No**. Fenced design only after real correlated receipt and DB persistence. | Actual v16.0.1 network scheduling and exact target build/bus behavior untested. |
| F02 duplicate activation | Same ID/payload returns prior result without another output write; ID reuse with changed payload rejects. | Host prototype **Model PASS**. | Not a stale case; duplicate does not reapply. | Only after actual fenced Stop receipt; model alone cannot release. | Cache bounds, authentication and real lost-ACK behavior untested. |
| F03 obsolete generation | Any command below persisted floor is rejected before mutation. | Host prototype **Model PASS**. | **Yes in model**. | Not real-firmware evidence. | Firmware/core gate absent. |
| F04 out-of-order sequence | Lower/equal sequence in the current generation rejects. | Host prototype **Model PASS** (sequence 2 before sequence 1). | **Yes in model**. | Not real-firmware evidence. | Concurrent core queue / cross-connection serialization absent. |
| F05 application crash after sending | Durable WLEDger ownership remains; recovery sends higher generation; controller rejects delayed old apply. | Existing reports establish durable app-side ownership; host prototype models late old command rejected after higher Stop. | **Yes in model only**. | No stock WLED release; retain ownership absent real fence receipt. | No firmware packet/request delayed across app process death. |
| F06 gateway crash | Gateway crash does not revoke controller command; higher device fence wins. | Host model shares the F05 newer-fence test; no gateway exists in this prototype. | **Yes in model only**. | No stock WLED release. | Gateway lifecycle/outbox not implemented. |
| F07 controller reboot | Durable epoch increments before any command/output/preset; prior-epoch commands reject, and activation remains blocked until current-epoch recovery Stop. | Host model reopens with the same in-memory journal, increments epoch, rejects old commands, blocks activation, then permits it after targeted recovery Stop. | **Yes in model**. | Not real reboot evidence. | No power cycling, NVS, bootloader/rollback or boot-preset test. |
| F08 power failure during fence persistence | No success ACK or protected mutation unless durable record is committed; corrupt/inconsistent state fails closed. | Host fake journal injects failed commit and checksum corruption; **Model PASS**. | **No new command applied in model**. | No; no durable hardware receipt. | Torn writes, NVS atomicity, filesystem damage and flash recovery untested. |
| F09 lost acknowledgement | Exact same-epoch retry is idempotent; after reboot old epoch rejects and WLEDger reconciles with new-epoch Stop. | Host model tests exact Stop retry and reboot epoch invalidation. | Old activation: yes in model. | Only after new-epoch Stop receipt on every target. | Network loss and response delivery not simulated. |
| F10 two competing WLEDger processes | Unique durable generations and device serialization ensure stale competitor cannot actuate after newer Stop. | Host model tests a stale generation after higher Stop; no concurrent processes or DB allocator are involved. | **Yes in sequential model**. | Not proven. | Actual cross-process generation allocation and simultaneous requests not implemented. |
| F11 three-controller partial delivery | Per-target progress is independent; one missing/ambiguous target keeps entire Locate ownership unresolved. | Host model requires three separate target receipts; existing Task 067 tests multi-controller loopback behavior without device fences. | **Yes only for the model’s already fenced targets**. | **No** until all potentially reached real targets confirm. | No three real devices, partial output completion, or durable WLEDger receipt integration. |
| F12 external JSON writer during Locate | Conflicting standard API write rejects or is isolated before protected state mutation. | Source review shows normal `/json` still calls core deserializer; no gate exists. **Architecture FAIL / unimplemented.** | **No current guarantee**. | **No**. | No WLED request run; no target external writers enumerated. |
| F13 preset/effect change during Locate | Internal preset/effect/transition writes cannot change fenced ranges or schedule later stale output. | Source review finds delayed preset processing and render/effect paths; no fence integration. **Architecture FAIL / unimplemented.** | **No current guarantee**. | **No**. | No firmware test; effects and transition cancellation unsolved. |
| F14 reconnect with outstanding commands | Reconnect alone is not a barrier; old epoch/generation rejects and recovery uses current epoch. | Host model tests old epoch after reboot; no real sockets/reconnect queue. | **Yes in model**. | Only after new-epoch Stop and output receipt. | Actual async network stack behavior remains untested. |
| F15 controller identity/output-layout mismatch | Mismatched identity, output fingerprint, unowned range or out-of-bounds range rejects before mutation. | Host prototype **Model PASS** for mismatch and bounds. | **Yes for fenced protocol commands in model**. | Mismatch must retain ownership; no current device handshake. | Fingerprint provisioning/authenticity and bus layout source are unresolved. |
| F16 firmware upgrade/replacement | Only verified fencing-capable build with preserved journal may resume; downgrade/replacement fails closed. | Host model tests stored identity/layout mismatch fail-closed at boot; source build/upgrade not run. | **Not established for real updates**. | **No** on stock, downgraded, reset or replacement controller. | OTA rollback, factory reset, anti-rollback, NVS survival and re-enrollment untested. |

## 11. Resource and performance feasibility

No WLED firmware measurements are available. The host model is STL-based and its RAM, binary size and timing are not representative of ESP32/ESP8266 firmware. No command throughput, JSON parse time, flash latency, output latency or effect-frame impact is claimed.

Source and design implications:

- Fence checks are constant-time in generation/sequence and identity comparisons; range validation is linear in command ranges/owned ranges. An implementation can set a compile-time maximum command range count and avoid dynamic allocation in the async receive path.
- WLED supports multiple platform-specific buses, and `strip.show()` can return while a driver is sending. A final completion receipt needs per-output completion aggregation; `strip.isUpdating()` is global/polled rather than a correlated command token.
- The WS source comments show complete text-frame limits vary by platform (1428 bytes on ESP32, 528 on ESP8266); HTTP/JSON capacity is separately build-configured. Envelope/range maximums cannot be chosen until the target build and transport are fixed.
- Durable writes should be per boot and generation/fence transitions, never per pixel or sequence. Exact endurance and write latency require the target module, storage partition, wear-leveling configuration and use rate.
- Persistence cannot be implemented by blocking an async networking callback for unbounded time. Use a bounded serialized executor; keep render/network/watchdog tasks responsive and send the correlated response asynchronously after persistence, targeted rendering and bus-completion events.
- Exact maximum range count, JSON overhead, queue memory and target throughput remain **UNMEASURED**. A compact authenticated envelope and bounded command parser are likely necessary on smaller ESP8266-class builds.

## 12. Architecture decision

| Option | Safety guarantee potential | Scope / deployment / compatibility | Decision |
|---|---|---|---|
| Standalone WLED usermod | Low: custom endpoint only fences its own requests; standard state, WS, internal effects, realtime and other inputs can bypass. | Low initial code, but no adequate API interception, boot ordering or durable completion receipt. | **Reject as sufficient.** Useful only for protocol/config/helper code after core integration exists. |
| Usermod plus small WLED core hooks | Conditional: possible if every state writer shares a queue, every protected mutation checks the generation, startup is gated, persistence is durable, and bus completion is correlated. | Moderate-to-high change across HTTP, WS, JSON/pixel mutation, preset/effect/realtime/input paths, startup and bus drivers; version/platform upkeep remains. | **Technically feasible candidate**, but hooks are not confined to one endpoint and still need exact-build/driver evidence. |
| Maintained WLED firmware fork | Highest control of the above, but no guarantee until bypasses, rollback and bus variants are tested. | High ongoing merge/rebase/build/release burden; must pin supported WLED versions, platforms, feature flags and buses. | **Recommended path for Task 067E prototype** if the owner selects a supported ESP32 target. Keep a thin usermod for settings/protocol plumbing and core-enforce semantics. |
| External gateway with stock WLED | No device-side stale-command rejection; a gateway cannot revoke work already accepted by WLED. | Easier centralized outbox/network policy; does not solve accepted stale state, WLED UI, presets or physical output completion. | **Insufficient alone.** Optional dispatcher only after controller support. |
| Alternative firmware/protocol | Potentially equivalent if it exposes durable epochs, apply-point checks, exclusive output ownership and bus-completion semantics. | Controller/firmware replacement and migration burden; same bypass and durability proof obligations still apply. | Reassess only if WLED core fork cost or driver APIs make the required contract impractical. |

### Recommendation and reduced-guarantee fallback

Proceed only with a **pinned WLED fork plus core hooks** for a first verified target. A usermod can register the command endpoint and provide configuration helpers, but must not claim enforcement for standard or internal WLED writers by itself. For the first target, choose an ESP32 board only after owner hardware identification; do not silently target the repository’s illustrative PCB/example topology.

If no firmware can provide complete ownership and a correlated drain receipt, the smallest defensible stock-WLED fallback is **no automatic ownership release after an uncertain activation**: keep the WLEDger target/session unresolved and block conflicting layout/controller changes until a human performs an approved, identity-checked recovery procedure. This reduces availability and still needs a separately reviewed way to establish a safe physical state; a quiet-period timeout, repeated GET or gateway is not an equivalent guarantee.

## 13. Verification, Task 067E scope, and physical decision

### Commands run

| Check | Result |
|---|---|
| `g++ -std=c++17 -Wall -Wextra -Werror -pedantic -fsanitize=address,undefined experiment/task067d-controller-fencing-poc/fence_state_machine_test.cpp -o /tmp/task067d-fence-test && /tmp/task067d-fence-test` | **PASS** — eight host test groups cover F01–F09, F11, F14 and F15 model cases. F10 is not a concurrency test; F12/F13 are source-reviewed bypass findings and F16 is only partly represented by identity/layout mismatch. The test explicitly reports it is not firmware evidence. |
| `python3 scripts/task067c_command_order_model.py` | **PASS** — all 17 reference-model assertions. This remains an architecture simulation. |
| `/home/spetchal/gotoolchain/go/bin/go test -count=1 -tags fts5 -run '^TestPhysicalLocateDelayedActivationCanRelightPixelsAfterAcknowledgedRecovery$' ./internal/wled` | **PASS as a failure reproducer** — unsafe post-ack relight is reproduced in the loopback mock. It is not safety acceptance. |
| `git diff --check` | **PASS** for tracked baseline changes; new-file checks and staged-file checks remain part of final verification. |
| WLED PlatformIO/ESP32 firmware build | **NOT RUN** — PlatformIO absent, standard ESP32/ESP8266 cores unavailable in Arduino CLI, exact target platform unknown. No dependencies installed. |
| WLED firmware network/output tests or physical tests | **NOT RUN / prohibited** — no source integration, no hardware authorization. |

### Proposed Task 067E implementation scope

1. Obtain the completed controller checklist and owner decision on one exact board, platform/core, flash layout, WLED tag/commit, bus driver, output mapping, transport and recovery method.
2. Pin a complete isolated WLED build, including PlatformIO/platform packages and every library commit; first prove a stock v16.0.1 target build reproducibly before patching. No device flash/OTA.
3. Implement a test-only WLED core patch/fork that routes HTTP, WS, presets, effects/transitions, realtime, buttons/IR/serial and usermods through a fenced serialized mutation/output policy. Gate boot preset/output until durable fence state is checked.
4. Implement target-specific dual-slot/NVS (or equivalent) journal, identity/build/layout handshake, fail-closed boot, power-cut fault injection and anti-rollback/factory-reset behavior. Keep protocol keys/secrets out of source control.
5. Add a controller-side renderer/bus completion receipt with explicit per-output semantics; test delayed/out-of-order commands, an in-progress frame, transitions, all supported inputs, and unrelated-pixel invariance on host and exact firmware builds.
6. Re-run all F01–F16 cases, including real firmware in an isolated emulator/test harness if available and then an explicitly authorized isolated bench. Do not integrate into WLEDger production Locate/Stop in 067E unless separately authorized after firmware semantics are accepted.
7. Obtain independent review before any controlled hardware trial. Keep production/staging and the physical WLED installation out of scope.

**Physical-testing decision: NO.** Physical WLED testing remains blocked until a selected protocol is implemented, independently reviewed, verified against the exact controller/firmware/output driver, and separately authorized for a controlled trial. This task did not contact, probe, flash, or OTA any controller.
