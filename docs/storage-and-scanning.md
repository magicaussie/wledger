# Storage Hierarchy & Scanning — Design Notes

Reference notes for evolving WLEDger's storage model and QR/barcode system.
Captures the desired model, the current implementation, the gaps, the locked
decisions, and exactly where in the code each piece lives so future changes take
scanning into account.

Status: **design agreed, implementation in progress.**

---

## 1. Desired model

```
Cabinet            (has a WLED controller assigned; 1 controller per cabinet)
  └── Drawer       (any number; each drawer maps to LEDs on one of the strings)
        └── Bin    (any number; LEDs are OPTIONAL on a bin)
              └── Item / Product   (QR or barcode)
```

Every level is scannable:

| Level   | Identifier | Code on the label    |
| ------- | ---------- | -------------------- |
| Cabinet | controller | QR                   |
| Drawer  | container  | QR                   |
| Bin     | bin        | QR                   |
| Item    | part       | QR **or** 1D barcode |

The system should be able to resolve "which cabinet / drawer / bin / item am I
in?" at any point — when locating, when moving stock, when creating/removing
items, and when scanning.

---

## 2. Current model (as implemented)

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

The **data model already supports the desired hierarchy**; the work is
terminology, QR coverage, scan routing, and LED states.

---

## 3. Locked decisions

1. **Terminology** — keep DB tables (`controllers`, `containers`, `bins`);
   rename **UI labels** only: Cabinet / Drawer / Bin. Scan codes use the
   user-facing terms. `Wall` stays as-is (a dashboard grouping of drawers).
2. **One controller per cabinet** for now; multiple strings (segments) per
   controller is already supported.
3. **Scanning a drawer with no active context** → highlight the drawer via LEDs,
   then open a **drawer action sheet** (add item to bin, add/remove/rename bin,
   move bin, view contents, edit drawer). If a context *is* active (e.g. a move
   modal), the scan sets the target instead — same pattern as the bin picker.
4. **Reading** must support **both 1D barcodes and QR**. **Generation** stays
   **QR-only**.
5. **Errors** flash the relevant bin/drawer **red** (e.g. unknown code, failed
   locate/move).
6. **LED colours + states (incl. flashing)** are a first-class, configurable
   system (see §7).

---

## 4. Current QR / barcode system

**Code scheme** (defined in `internal/handler/parts.go`):

- `wledger:bin:<id>`  → `/parts?bin=<id>` (show bin contents)
- `wledger:part:<barcode|id>` → exact part page, else search
- plain barcode → exact part match, else search

**QR generation:** `internal/qrcode/qr.go` (`PNG(content, scale)`).

**QR endpoints:**
- `GET /parts/{id}/qr` → `HandlePartQR` (`internal/handler/parts.go`)
- `GET /bin/{id}/qr`   → `HandleBinQR` (`internal/handler/hardware.go`)

**Scan resolver (server):** `HandleScan` at `GET /scan?q=` (`internal/handler/parts.go`).

**Scan routing (client):** `web/static/js/scan_router.js` listens for the
`fastscan` event and routes `wledger:bin:` / `wledger:part:` / plain codes.
Inputs: `fast_scan.js` (keyboard-wedge USB/BT scanners) and `barcode_scanner.js`
(webcam via html5-qrcode). Both dispatch the same `fastscan` event.

**Label sheets:**
- `GET /parts/labels` → `HandleProductLabels` → `web/pages/product_labels.templ`
- `GET /hardware/labels` → `HandleBinLabels` → `web/pages/bin_labels.templ`

**Bin picker:** `web/components/bin_picker_grid.templ` + `bin_picker_card.templ`;
`scan_router.js` can select a bin in the open picker when a bin QR is scanned
(`window.__binPickerContext`).

---

## 5. Gaps vs the desired model

1. **No QR for cabinets (controllers) or drawers (containers).**
2. **Scan routing only knows `bin` and `part` prefixes.**
3. **Terminology mismatch** (addressed by §3.1).
4. **LED-less bins** are representable but not locatable (expected).
5. **Scanning is not context-aware everywhere.**
6. **No 1D barcode generation** (not needed — QR-only generation is fine).
7. **No LED flashing / state system** (see §7).

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
