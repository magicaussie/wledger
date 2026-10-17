> **HISTORICAL / IMPLEMENTATION TRACKING — NOT AUTHORITATIVE TARGET DESIGN (2026-10-17).**
> Owner-approved target requirements are in [docs/PRODUCT_SPEC.md](PRODUCT_SPEC.md). Conflicting statements below (including one-controller cabinets, bin-owned LEDs, multiple drawer pixel ranges, and QR-only label generation) are superseded. Do not use this file as instructions for new architecture. Check source/tests for actual implemented status; older completion claims may refer to a different commit.

# Storage Hierarchy & Scanning — Design Notes

Reference notes for evolving WLEDger's storage model and QR/barcode system.
Captures the desired model, the current implementation, the gaps, the locked
decisions, and exactly where in the code each piece lives so future changes take
scanning into account.

Status: **physical QR and Code 128 identities use stable entity IDs; scan routing keeps legacy electrical identities distinct and stock-move scans stay in their active operation context.**

---

## 1. Desired model

```
Cabinet            (has a WLED controller assigned; 1 controller per cabinet)
  └── Drawer       (any number; each drawer maps to LEDs on one of the strings)
        └── Bin    (any number; LEDs are OPTIONAL on a bin)
              └── Item / Product   (QR or barcode)
```

The physical inventory hierarchy and legacy electrical hierarchy have separate
identities. New physical cabinet/drawer labels never reuse controller/container
codes. Bins retain their stable `bins.id`; parts use stable `parts.id` for newly
generated QR codes, while existing barcode-based codes remain readable.

| Entity | New generated code | Destination |
| --- | --- | --- |
| Physical cabinet | `wledger:physical-cabinet:<id>` | `/storage/cabinets/<id>` |
| Physical drawer | `wledger:physical-drawer:<id>` | `/storage/drawers/<id>` |
| Physical bin | `wledger:bin:<id>` | `/parts?bin=<id>` |
| Part | `wledger:part-id:<id>` | `/parts/<id>` |
| Legacy controller | `wledger:cabinet:<id>` | `/hardware/<id>/grid` |
| Legacy container | `wledger:drawer:<id>` | `/drawers/<id>` |

The system should be able to resolve "which cabinet / drawer / bin / item am I
in?" at any point — when locating, when moving stock, when creating/removing
items, and when scanning.

---

## 2. Legacy electrical model (as implemented)

The diagram and terminology mapping below describe the retained electrical
controller/container records, not physical inventory placement. Physical storage
uses separate `cabinets` → `drawers` → `bin_drawer_attachments` → `bins` and
`part_assignments` → `parts` records; QR identities keep those hierarchies
separate.

```
Controller  (WLED device: ip, port, led_count)
  └── Container   (belongs to ONE controller; segment_id + layout config; position_index)
        └── Bin    (belongs to ONE container; led_index [nullable], width, grid_x/grid_y, name)

Wall  (display-only grouping of containers; no LED meaning)
```

- `bins.led_index` is **nullable** and `UNIQUE(container_id, led_index)` allows
  multiple NULLs — so **bins without LEDs are already representable**.
- A container's LED span is derived from its layout config
  (`linear` / `grid` / `compound`) via `mapper.GetContainerLength`.
- A controller can host **many containers**, each with its own `segment_id`
  ("string"). So "multiple strings per cabinet" already works.

### Mapping to the desired model

| Desired | Current    | Notes |
| ------- | ---------- | ----- |
| Cabinet | Controller | cabinet *is* the controller (1:1) |
| Drawer  | Container  | has `segment_id` + LED layout |
| Bin     | Bin        | LED optional (already supported) |
| Item    | Part       | `parts.barcode_data` |

The legacy electrical tables support the controller/container/bin mapping.
Physical cabinet, drawer and bin placement is represented by the separate
physical-storage tables; QR prefixes and destinations preserve that distinction.

---

## 3. Locked decisions

1. **Terminology** — keep DB tables (`controllers`, `containers`, `bins`);
   rename **UI labels** only: Cabinet / Drawer / Bin. Scan codes use the
   user-facing terms. `Wall` stays as-is (a dashboard grouping of drawers).
2. **One controller per cabinet** for now; multiple strings (segments) per
   controller is already supported.
3. **Scanning a physical drawer with no active context** opens its physical
   storage detail. During an active stock operation, a drawer scan narrows the
   existing destination picker instead of navigating away; the user then selects
   a bin. Scanning does not require WLED output.
4. **Reading and label generation** must support both QR and standard 1D Code 128 using the same stable identity for physical cabinets, drawers, bins and products.
5. **Errors** must be visible in the active workflow (e.g. unknown code,
   mismatched source/product, unavailable destination). Error reporting must work
   with WLED output disabled; optional LED signaling is outside this scanning path.
6. **LED colours + states (incl. flashing)** are a first-class, configurable
   system (see §7).

---

## 4. Current QR / barcode system

**Code scheme** (defined in `internal/handler/parts.go`):

- `wledger:physical-cabinet:<id>` → `/storage/cabinets/<id>`
- `wledger:physical-drawer:<id>` → `/storage/drawers/<id>`
- `wledger:bin:<id>` → `/parts?bin=<id>` (stable physical inventory-bin identity)
- `wledger:part-id:<id>` → `/parts/<id>` (stable part identity used by generated QR labels)
- legacy `wledger:cabinet:<id>` → `/hardware/<id>/grid` (controller; unchanged)
- legacy `wledger:drawer:<id>` → `/drawers/<id>` (container; unchanged)
- legacy `wledger:part:<code>` and plain barcode → exact barcode match, else search

**QR generation:** `internal/qrcode/qr.go` (`PNG(content, scale)`). Code 128 generation uses the same stable scan-code helpers and the `github.com/boombuler/barcode` encoder.

**QR endpoints:**
- `GET /parts/{id}/qr` → part ID QR (`HandlePartQR` in `internal/handler/parts.go`)
- `GET /storage/cabinets/{id}/qr` → physical cabinet QR (`HandlePhysicalCabinetQR`)
- `GET /storage/drawers/{id}/qr` → physical drawer QR (`HandlePhysicalDrawerQR`)
- `GET /storage/bins/{id}/qr` and legacy `/bin/{id}/qr` → stable bin QR
- Legacy `/cabinet/{id}/qr` and `/drawer/{id}/qr` continue producing controller/container identities.
- `GET /scan/barcode/{type}/{id}` renders Code 128 for `bin`, `physical-cabinet`, `physical-drawer` and `part` identities only.
- `/labels/{type}/{id}` renders a printable label containing both formats; product batch labels include every product, not only products with a manufacturer barcode.

**Scan resolver (server):** `HandleScan` at `GET /scan?q=` and its typed JSON counterpart `GET /scan/resolve?q=` (`internal/handler/scan_resolver.go`).

**Scan routing (client):** `web/static/js/scan_router.js` sends typed physical,
legacy, bin and part IDs to `/scan`; `HandleScan` validates entity existence and
redirects. Direct entity pages perform their own database-backed validation,
returning 400 for malformed IDs and 404 for absent records. Inputs:
`fast_scan.js` (keyboard-wedge USB/BT scanners) and `barcode_scanner.js` (webcam
via html5-qrcode with QR and Code 128 enabled) dispatch the same `fastscan` event.

**Printable labels:**
- `GET /labels/{type}/{id}` → `HandleScanLabel` → `web/pages/scan_label.templ`; prints both identity formats for each cabinet, drawer, bin or product.
- `GET /parts/labels` → `HandleProductLabels` → `web/pages/product_labels.templ`; product manufacturer barcode data remains separate from the generated internal Code 128.
- `GET /hardware/labels` remains the legacy electrical controller/container/bin QR sheet and is not repurposed for physical cabinets or drawers.

**Bin picker and stock movement:** `web/components/storage_bin_picker.templ` uses stable `bin_id` selection. `stock_move_scan.js` registers a context only while a Move Stock dialog is open; source/product scans are validated through `/scan/resolve`, cabinet/drawer scans narrow the active picker, and destination-bin scans select within that location. Manual picker selection remains available.

---

## 5. Remaining gaps vs the broader design

1. **LED-less bins** are representable but not locatable (expected).
2. **No LED flashing / state system** (see §7).

Physical cabinet, drawer, bin and part QR navigation is implemented independently
of Locate. Generated entity QR endpoints reject malformed, unknown or deleted
identities; scanner routing preserves the legacy controller/container prefixes.

---

## 6. Scanner compatibility

The user's scanner is a **Bluetooth barcode reader with a dongle**, which
presents as a **USB HID keyboard** (keyboard-wedge) — handled by
`web/static/js/fast_scan.js`. No BT-specific code is required.

Things to harden in `fast_scan.js`:

- **Terminator:** currently only `Enter` fires a scan. Accept **Tab / CR / LF**
  too, since some scanners use those as suffixes.
- **Inter-key timing:** `MAX_INTERVAL = 50ms` is hard-coded. **BT HID is often
  slower than USB**, so a slow scanner can be misread as manual typing. Make the
  interval **configurable** (setting or `data-` attribute).
- **Prefix:** optionally strip a configurable scanner prefix before routing.

---

## 7. LED colours & states

Today `wled.Client.LightUp` only sets a **solid** colour. Proposed:

- A **state abstraction**: `{ Color, Mode }`, `Mode ∈ {solid, flash}` (extensible
  to breathe/pulse later).
- Client methods: `Apply(ctx, ip, segmentID, index, count, state)` and
  `Flash(...)` (toggle colour/off N times over a bounded duration).
- **Settings** for named states — extend the existing colour settings
  (`color_locate`, `color_stock_ok/low/critical`) with `color_error` and a
  per-state mode. Store as a small JSON blob in `settings` rather than many
  columns.
- Wire states into: **locate** (solid blue), **success** (solid green),
  **error** (flash red), and the **stock status** colours.

**Flashing implementation:** server-side (bounded goroutine toggling the `i`
API) rather than WLED effect IDs, which vary by firmware version. Reliable and
version-independent; can move to device-side effects later if needed.

---

## 8. Code map — where scanning must be considered

### Storage model
- `sql/schema/001_init.sql` — `controllers`, `bins`
- `sql/schema/004_multi_container_hierarchy.sql` — `containers`, `walls`, `wall_cards`
- `sql/schema/006_add_container_position.sql` — `containers.position_index`
- `internal/db/models.go` — `Controller`, `Container`, `Bin`, `Wall`, `WallCard`
- `internal/hardware/service.go` — `CreateController`, `SaveGrid`
- `internal/hardware/mapper/mapper.go` — `CalculateGlobalIndex`, `GetContainerLength`
- `web/components/grid_painter.templ`, `web/static/js/grid_painter.js`
- `internal/handler/walls.go`, `internal/dashboard/service.go`

### QR / barcode
- `internal/qrcode/qr.go` — QR encoder
- `internal/handler/parts.go` — scan prefixes, `BinScanCode`, `PartScanCode`,
  `HandlePartQR`, `HandleScan`, `HandleProductLabels`
- `internal/handler/hardware.go` — `HandleBinQR`, `HandleBinLabels`
- `web/pages/product_labels.templ`, `web/pages/bin_labels.templ`
- `web/static/js/scan_router.js`, `fast_scan.js`, `barcode_scanner.js`
- `web/components/bin_picker_grid.templ`, `bin_picker_card.templ`

### Stock moves / item CRUD
- `internal/stock/service.go` — `AssignStock`, `MoveStock`, `RemoveStock`, `AdjustStock`
- `internal/handler/parts.go` — `HandlePartAssign`, `HandlePartStockMove`,
  `HandlePartStockRemove`, `HandlePartStockAdjust`
- `web/components/move_part_modal.templ`
- `parts.barcode_data` — the item barcode field (unique)

### Locate / LED
- `internal/wled/client.go` — `LightUp`, `Clear`, `SetState`
- `internal/wled/service.go` — `LocatePart`, `LocateBin`
- `internal/hardware/mapper/mapper.go`

---

## 9. Proposed scan code scheme

| Code                   | Resolves to |
| ---------------------- | ----------- |
| `wledger:cabinet:<id>` | cabinet (controller) view / filter |
| `wledger:drawer:<id>`  | drawer (container) view / action sheet |
| `wledger:bin:<id>`     | bin contents (exists) |
| `wledger:part:<code>`  | exact part (exists) |

---

## 10. Build order

1. **LED state system** — client `Apply`/`Flash` + settings (foundation).
2. **Cabinet + Drawer QR** — endpoints, labels, scan-code routing.
3. **Scan-a-drawer UX** — highlight + action sheet; error flashing.
   - [x] Highlight: scanning `wledger:drawer:<id>` opens the dedicated drawer
     page (`/drawers/{id}`), which lights the drawer's LEDs on load.
   - [ ] Action sheet (add/remove/rename bin, move bin, edit drawer).
   - [ ] Error flashing on unknown code / failed locate.
4. **Scanner hardening** — `fast_scan.js` terminator/timing/prefix; webcam 1D
   formats + wider scan box.

---

## 11. Remaining open questions

- Flashing duration: bounded (flash N× then off) vs persistent until next action?
- Drawer action sheet: modal on the current page vs a dedicated drawer page?
  → Resolved: a dedicated drawer page (`/drawers/{id}`). It highlights the
  drawer's LEDs on load and lists its bins/contents; the action sheet can be
  layered onto this page later.
