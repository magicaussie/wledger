# WLEDger Roadmap / Ideas

A working list of functionality gaps and improvements identified during a code
review. Items are grouped by priority. Each entry notes the rationale and the
concrete hook points in the codebase so it can be picked up independently.

Legend: `[ ]` not started · `[~]` in progress · `[x]` done

---

## Priority 1 — High value

### [x] Reorder / low-stock workflow

**Why:** `reorder_level` and `min_stock_threshold` are stored on every part and
used to colour bins (`internal/dashboard/service.go`) and show a single warning
on the part detail page — but there is no aggregated view of what needs
reordering, and no path from "I'm low" to "order it". This is the core value of
an inventory system and is currently missing.

**Scope:**
- [x] New "Low Stock / Reorder" page listing parts at/below `reorder_level`
      (with a separate critical tier at `min_stock_threshold`) and a suggested
      order quantity.
- [x] Export the list to CSV/clipboard using the same columns as the import
      template so it round-trips.
- [x] Per-row "find supplier" action that deep-links into the existing supplier
      search (`/suppliers/search`) using the part number.

**Implemented:**
- Query `ListLowStockParts` in `sql/queries/parts.sql` (excludes parts with no
  threshold set; ordered by largest shortfall).
- `parts.Service.ListLowStock` + `web/pages/low_stock.{go,templ}` view/page.
- Handlers `HandleLowStock` / `HandleLowStockExport` in
  `internal/handler/parts_low_stock.go`; routes in `internal/router/router.go`.
- Sidebar link; English i18n keys; supplier page now accepts `?q=` to pre-fill
  and auto-run a search.
- Test `TestListLowStockParts` in `internal/db/parts_low_stock_test.go`.

**Hook points:**
- Query: `sql/queries/parts.sql` (add a low-stock query).
- Route: read group in `internal/router/router.go`.
- Page: `web/pages/` (new templ page + sidebar link).

---

### [x] Favorites are half-implemented (bug)

**Why:** `parts.is_favorite` exists, is restored in backups, is counted in
dashboard stats, and renders a ★ in `web/pages/parts.templ` — but there is **no
route or handler to ever set it**. It is currently dead data.

**Scope (pick one):**
- [x] Wire up a toggle: `POST /parts/{id}/favorite` with an HTMX swap on the
      star, or
- [ ] Remove the field entirely.

**Implemented:**
- Query `TogglePartFavorite` in `sql/queries/parts.sql` (atomic flip, returns
  the new state).
- `parts.Service.ToggleFavorite`.
- `HandlePartFavorite` in `internal/handler/parts.go`; route in the
  editor/admin group in `internal/router/router.go`. Returns the swapped button
  for HTMX requests, otherwise redirects back to the part.
- `components.PartFavoriteButton` + `icons.Star`; used on the parts list cards
  and the part detail header. Read-only users see a static star.
- Tests: `TestTogglePartFavorite`, `TestPartFavoriteButtonRenders{,ReadOnly}`.

**Hook points:**
- Handler: `internal/handler/parts.go`.
- Route: editor/admin group in `internal/router/router.go`.
- UI: `web/pages/parts.templ`, `web/pages/part_detail.templ`.

---

### [ ] Write-capable API + MCP tools

**Why:** `/api/v1` is deliberately read-only + locate (see
`docs/api-and-mcp.md`). For the Home Assistant / voice use case it was built
for, the natural commands are missing: "add 5 of the 10k resistors", "I used one
of these", "what do I need to reorder?".

**Scope:**
- [ ] `POST /api/v1/parts/{id}/stock` — adjust/assign stock.
- [ ] `POST /api/v1/parts` — create a part.
- [ ] MCP tools: `adjust_stock`, `create_part`, `find_low_stock`.

**Hook points:**
- API: `internal/api/api.go` (routes), `internal/api/handlers.go`.
- MCP: `cmd/mcp-server/tools.go`.
- Reuse existing transactional methods in `internal/stock/service.go`
  (`AdjustStock`, `AssignStock`).

---

## Priority 2 — Valuable

### [ ] Cabinet / Drawer QR codes + scan coverage

**Why:** The storage hierarchy is Cabinet (controller) → Drawer (container) →
Bin → Item, and every level should be scannable. Today only bins and parts have
QR codes; cabinets and drawers have none, and the scan router only understands
`wledger:bin:` / `wledger:part:`. See `docs/storage-and-scanning.md` for the
full design notes and a code map of everywhere scanning must be considered.

**Scope:**
- [ ] QR endpoints + labels for cabinets (controllers) and drawers (containers).
- [ ] Extend the scan code scheme (`wledger:cabinet:`, `wledger:drawer:`) in
      `HandleScan` and `web/static/js/scan_router.js`.
- [ ] Make scanning context-aware for moves/CRUD (scan a drawer/cabinet as the
      target context).
- [ ] Decide terminology (rename UI labels vs DB tables) — see open questions.

**Hook points:** `internal/handler/parts.go`, `internal/handler/hardware.go`,
`web/static/js/scan_router.js`, `web/pages/bin_labels.templ`.

### [ ] Notifications / webhooks

**Why:** There is no alerting of any kind (no email, webhook, or HA
notification). A simple outbound webhook fired when a part crosses into
`critical` (and optionally on locate) would unlock automation.

**Scope:**
- [ ] New `internal/notify` package.
- [ ] Settings field for the webhook URL (follow the existing settings pattern).
- [ ] Fire on critical-stock transition; optionally on locate.

**Hook points:**
- `internal/settings/`, `internal/stock/service.go`, `internal/wled/service.go`.

---

### [ ] Scheduled price snapshots + price-drop alerts

**Why:** `price_history` and `RecordPriceSnapshot` exist, but snapshots only
happen on import or via a manual endpoint. Without a background job, price
history stays sparse and is not useful.

**Scope:**
- [ ] Background scheduler (goroutine + ticker, or an on-demand "refresh all
      tracked parts" action) that snapshots periodically.
- [ ] Flag/notify on price drops.

**Hook points:**
- `internal/suppliers/service.go` (`RecordPriceSnapshot`),
  `internal/db/price_history.sql.go`.

---

### [ ] BOM / project workflow

**Why:** There is no notion of a project or bill-of-materials. For makers this
is a killer feature: build a BOM, check it against stock, see what's missing,
and export a shopping list (ties into the reorder workflow and the supplier
layer).

**Scope:**
- [ ] Project/BOM entity + CRUD.
- [ ] Availability check against current stock.
- [ ] Missing-quantity calculation + shopping-list export.

**Hook points:**
- New migration in `sql/schema/`, queries in `sql/queries/`, service + handlers,
  new page in `web/pages/`.

---

## Priority 3 — Smaller completions

### [ ] Implement `BatchProvider`

**Why:** `BatchProvider` is defined in `internal/suppliers/provider.go` but
implemented by no provider. Multi-keyword/BOM search currently does N sequential
provider calls.

**Scope:**
- [ ] Implement `SearchByKeywordsBatch` for the API-based providers
      (DigiKey, Mouser, etc.).

---

### [ ] 1D barcode generation (Code128 / EAN)

**Why:** Scanning supports 1D barcodes, but label generation is QR-only
(`internal/qrcode/qr.go`). Users with 1D scanners are underserved.

**Scope:**
- [ ] Add Code128/EAN rendering and a label option.

---

### [ ] Provider test coverage

**Why:** Only Spotlight has a (manual) test. With 26 providers, regressions are
easy to miss.

**Scope:**
- [ ] Table-driven tests for `ExtractPartIDFromURL` and response parsing across
      providers.

---

### [ ] "Update existing part from supplier"

**Why:** Import rejects duplicates (`ErrPartAlreadyImported`). A merge /
refresh-price path would let users update an existing part from a supplier URL.

**Scope:**
- [ ] Merge/refresh flow in `internal/suppliers/service.go`
      (`ImportFromProvider`).

---

## Robustness / cleanup

- [ ] `sessionManager.Cookie.Secure = false` — set to `true` in production
      (`cmd/server/main.go`, has a TODO).
- [ ] bcrypt cost mismatch: `internal/auth` uses cost 12, `internal/settings`
      uses `bcrypt.DefaultCost` (10). Align them.
- [ ] `internal/wled/client.go` `Clear` hardcodes 5000 pixels — make it dynamic
      based on actual LED/segment config.
- [ ] `system_flags` table is essentially unused outside tests — use it or drop
      it.
- [ ] Backup export has a TODO for the "zip uploads failed" error path
      (`internal/backup/service.go`) — surface a UI error/toast.
