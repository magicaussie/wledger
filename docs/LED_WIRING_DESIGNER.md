# Graphical LED Wiring Designer

## Entry points

Hardware → **Add WLED controller** starts guided setup at `/hardware/new`.
Existing controller cards use **Edit Wiring**, opening the same designer at
`/hardware/{controllerID}/wiring-designer`. The selected controller in LED Test
Centre also has **Open Graphical Wiring Designer**. The old Add Controller modal
has been removed; its raw configuration form is not the primary setup path.

## Guided controller setup

1. Enter only the controller name, IP address/hostname and port.
2. Optionally click **Discover Controller**. This explicit administrator action
   performs bounded `GET /json/info` and `GET /json/cfg` requests, without WLED state
   writes or controller configuration changes. Redirects are not followed. Output
   counts, firmware, pins and hardware direction are shown for review; hardware
   reversal is not silently applied to application addressing.
3. Choose **Continue to Wiring Designer**, even when discovery is unavailable.
   This opens an unsaved graphical draft; no controller row is created yet.
4. Add outputs and set their names/pixel counts directly beside the canvas. Select
   visual templates and unequal row/column lengths without Rows × Columns arithmetic
   or electrical permutations. Connection type/timeout are optional Advanced settings.
5. **Save controller and layout** commits the controller, real output identities and
   complete drawing atomically. Any validation, duplicate endpoint or storage failure
   rolls back the whole save and retains the draft for correction/retry.

Draft output identities exist only in browser memory and are remapped to real IDs
inside the transaction. New outputs retain linear physical addressing; serpentine
visual placement never rewrites pixel identities. Existing controllers keep the
previous protected visual-save and explicit electrical-review workflows. Legacy
technical editing remains available only through the Advanced controller settings
link. Hardware connection badges no longer poll automatically: status reads require
an explicit **Check connection** click.

The designer is local application configuration, not WLED firmware configuration.
Opening it, previewing, arranging or saving drawings never contacts a controller.
Existing controller output records, cabinet topology, drawer allocations and bin
assignments remain authoritative.

## Arrange Layout (default)

1. Select an output rectangle or pixel.
2. Choose Straight, Rectangle, or Irregular rows. Select Serpentine columns to
   arrange variable-length columns instead. Edit individual row/column lengths;
   the last other row balances the total so addresses and output capacity stay fixed.
3. Split an output into strip sections by their pixel counts. Drag the orange
   section labels or edit section offsets/rotation to position independent strips.
   Drag an output rectangle to move its group. Output rotation is in 90° steps;
   sections can have arbitrary rotation.
4. Enable Custom pixels to drag individual pixel positions. This changes drawing
   coordinates only, never their output mapping.
5. Add zero-pixel jumper paths by clicking canvas points; Enter/double-click or
   Finish jumper ends the path. Escape cancels it. Jumpers are drawing annotations,
   not additional addressed LEDs, WLED segments or proof of electrical connectivity.
6. Drag empty canvas to pan; use the zoom buttons or mouse wheel to zoom.
7. Optionally load a PNG/JPEG cabinet photograph or raster drawing. Adjust its
   position, size and opacity. Backgrounds are limited to 1 MiB, 4096 pixels per
   dimension and 16 megapixels; remote URLs, SVG and PDF are not accepted.
8. Save graphical layout. Reopening retains geometry, section transforms, custom
   pixel positions, jumpers and the embedded background.

Numbers beside pixels are zero-based physical offsets within the output. Arrows
follow physical output order. START/END refer to the physical output endpoints;
strip boundaries interrupt the line. Large outputs sample the displayed pixels
(up to roughly 3000 per output) to keep the drawing bounded.

## Pixel inspector and guided mapping

Selection uses the existing server-side output preview and cabinet topology
resolver. It shows controller identity, configured output ID, output logical
index (1-based), physical output offset (0-based), absolute controller pixel
(0-based), cabinet addresses (1-based), drawer-relative offsets and explicit bin
overrides when available. Cabinet addresses remain visible for unallocated pixels.
Configured output identities must not be confused with GPIO output identities or
WLED segments. No live GPIO/firmware verification is inferred from this drawing.

**Test selected pixel in LED Test Centre** opens the saved controller and absolute
pixel selection. It does not inspect or light anything automatically. Existing
Test Centre discovery, drawer endpoints, allocation preview/confirmation and bin
mapping workflows remain in place. Physical tests still require explicit operator
action and the physical-output safety gate; this feature does not enable it.

## Edit Wiring (administrators)

Electrical configuration is a separate draft. Administrators can add/remove/reorder
outputs, edit counts and directions, choose rectangular serpentine transforms or
an explicit custom physical order. Advanced custom permutations remain available
as a text field; ordinary physical positioning does not require one.

1. Select **Edit Wiring**.
2. Change the electrical draft, then **Review wiring**.
3. Inspect old/new settings and changes. Nothing has been applied yet.
4. Type **APPLY WIRING** and apply the reviewed configuration.

The backend repeats validation, checks the reviewed topology signature and takes
the existing cross-process operation claim before saving. Existing output IDs are
retained. Changes are rejected during active/unresolved Locate ownership or an
emergency barrier. For preservation, this designer conservatively blocks electrical
changes when the controller belongs to a cabinet or has legacy mapped bins. It does
not automatically migrate existing ranges or bin overrides. Arrange Layout remains
available for those installations.

The software configuration does not program WLED GPIOs or prove an output's real
hardware capacity. No hardware discovery is performed implicitly.

## Persistence and backup

Versioned, bounded graphical metadata is stored in
`system_flags` under `led_wiring_designer_v1:{controllerID}`. This is additive;
no schema migration or automatic conversion of existing electrical data is needed.
The existing backup includes these flags, including raster image data. Restore
retains the complete graphical document. Invalid geometry, duplicate pixel
references, overlapping/missing section coverage and unsupported image data are
rejected on save.

Visual revisions reject simultaneous stale saves. A separate electrical topology
signature is checked inside the transaction, so a draft opened before output
reconfiguration cannot overwrite its visual metadata without reloading.

## Verification

- `npm test` includes pure geometry, column/row traversal, physical display order,
  custom coordinates and electrical draft/payload tests.
- `npm run test:wiring-designer-browser` builds a disposable app, supplies local
  Alpine, blocks external browser requests and uses a loopback controller fixture.
  It verifies zero controller requests during drawing/selection, custom dragging,
  transforms, background/jumper persistence, unchanged mappings, stale saves,
  Test Centre handoff, rejected mapped electrical edits, typed confirmation and
  an eligible unmapped reversal preserving output identity.
- Go tests cover geometry/image limits, section coverage, stale topology and visual
  revisions, canonical unallocated pixel addressing (linear/reverse/serpentine/custom),
  role/CSRF boundaries and backup/restore of usable graphical metadata.

No deployment or physical hardware commissioning is implied by these tests.
