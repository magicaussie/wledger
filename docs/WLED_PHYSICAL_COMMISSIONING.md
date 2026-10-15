# Physical WLED output commissioning

Physical output is an operator-controlled acceptance step. Software tests and
loopback mocks cannot prove controller firmware behavior, actual wiring, strip
order, power integrity, or that the configured cabinet pixels correspond to the
intended physical drawer. Do not enable output or contact real controllers until
the installation owner has explicitly authorized the commissioning window.

## Before enabling output

1. Back up the WLEDger database and uploads, and record the deployed revision
   and migration state. Keep `WLEDGER_PHYSICAL_OUTPUT_ENABLED=false`.
2. Record each controller's model, WLED firmware version, MAC address, address,
   port, configured LED capacity, power supply, and the physical strip/output
   wiring. Use unique controller endpoints and confirm that each controller is
   assigned to only one cabinet. Current cabinet membership is limited to five
   controllers.
3. Restrict controller network access to the WLEDger service and authorized
   commissioning clients. WLED's ordinary JSON API does not provide WLEDger
   with an exclusive-writer lock; other clients can change LED state outside the
   application's session ownership.
4. In the hardware editor, use the controller output discovery action. It reads
   `/json/info` and `/json/cfg` only. Confirm the reported MAC and LED count,
   output order, explicit starts, lengths and active GPIO pins against the
   controller configuration and physical wiring. The saved controller capacity
   must equal WLED's reported pixel count.
5. The safe Locate mapper currently accepts only a continuous controller-wide
   RGB order: output starts must be explicit and contiguous; lengths must be
   positive and total the configured capacity; active pins must be present and
   unique; output reversal and skipped LEDs, RGBW, matrix mode and custom LED
   maps are unsupported. During Locate, `/json/state` must report complete,
   gap-free segment coverage; each targeted segment must be on, solid and
   ungrouped/unspaced, with no reversal, mirror or offset. Resolve any mismatch
   rather than bypassing the verification.
6. Configure cabinet controller order, physical string/path geometry and each
   drawer's one or more non-overlapping LED allocations. These are separate
   mappings: a physical output is not assumed to be a WLED segment or a drawer.
   Pixel allocations use 1-based inclusive controller/cabinet numbering in the
   editor; the application translates them to the controller's actual
   zero-based, segment-local WLED ranges. Verify every drawer/bin attachment and
   product stock location by stable IDs. Do not infer or convert old electrical
   container mappings into physical drawer geometry.
7. Save mapping verification for every controller used by a drawer. It is a
   read-only fingerprint of `/json/info` and `/json/cfg`; each Locate additionally
   reads `/json/state` and refuses a changed or unsupported segment layout.

## Authorized commissioning window

1. Confirm there is no active or partial Locate session. If a session may own
   pixels, use its Stop/recovery path and verify cleanup before changing any
   mapping. A controller configuration change deliberately blocks cleanup when
   the saved target can no longer be verified.
2. If the owner authorizes the hardware test, explicitly set
   `WLEDGER_PHYSICAL_OUTPUT_ENABLED=true` in the WLEDger deployment environment
   (or `.env` when using Compose), then recreate the `wledger` service. The
   application defaults to disabled. Keep controller master brightness low
   using the controller's own controls; WLEDger's Locate color level is fixed
   and is not a substitute for low-brightness commissioning.
3. Start with one known controller, one mapped drawer and a disposable or
   otherwise safe inventory bin. Confirm the selected pixels physically match
   the saved drawer allocation and no adjacent/unallocated pixels change.
   Verify the controller acknowledges each state command.
4. Press Stop and confirm only the session's saved ranges are commanded off.
   Stop writes black to those pixels; it does not restore their previous color
   or effect. Verify unrelated pixels remain unchanged.
5. Test other controllers and a drawer spanning controller boundaries only
   after each individual controller mapping has passed. Verify each affected
   controller's pixel indices and targeted Stop behavior independently.
6. Test Global Off only when its device-wide effect is explicitly acceptable.
   It sends `{"on":false,"live":false,"tt":0}` to every configured
   controller and turns off unrelated LEDs as well. It is rejected while a
   physical Locate session owns pixels.
7. Exercise an authorized offline/reconnect and WLEDger restart test. An
   unacknowledged command or changed layout must be reported and retain unresolved
   session ownership; do not edit mappings or issue another writer's commands
   until the selected cleanup/recovery outcome is understood.
8. Record firmware versions, controller identities, verified output layouts,
   tested drawer/bin IDs, the observed selected pixels, Stop/Global Off outcomes,
   and recovery results. Re-disable physical output after testing if routine
   output is not yet approved.

## Unverified without real-device testing

This repository's integration tests use disposable SQLite databases and
loopback HTTP fixtures. They verify HTTP paths, JSON, offsets, mapping rejection,
acknowledgements, timeouts, recovery and multi-controller behavior in software;
they do not establish that a particular WLED firmware/controller returns the
same response shape, that a particular GPIO drives the expected strip, that
physical pixels are wired in the recorded order, that external writers are
absent, or that power/network behavior is reliable. Those facts require the
owner's controlled commissioning procedure above. No real controller should be
contacted as part of automated acceptance.
