# WLEDger — Existing UI Design System

This document describes the design system that **already exists** in the WLEDger
web UI. It is written by inspecting the source, not by proposing a new one.

It exists so that new pages (including the M6 browsing pages) inherit the
current system instead of inventing a parallel one. Every value below is
observed in the repository; source file paths are cited as evidence.

- Stack: Tailwind CSS v4 (`package.json`: `tailwindcss ^4.1.18`) with the
  Tailwind CLI build (`npm run build:css`).
- Component library: daisyUI v5 (`package.json`: `daisyui ^5.5.13`), configured
  as a Tailwind plugin in `web/static/css/input.css`.
- Templates: Go + templ, in `web/layouts`, `web/components`, `web/pages`.
- Icons: hand-authored SVG templ files under `web/icons/`.

**Rule for M6 and all new pages:** compose the shared Templ primitives in
section 5 and reuse the daisyUI/Tailwind classes catalogued here. Do not
introduce new tokens, a new color system, a new component library, or a new
layout shell. Where this document records an inconsistency (section 8), match
the dominant existing pattern rather than inventing a third one.

---

## 1. Color & theme tokens

**Source of truth:** `web/static/css/input.css` and the runtime theme logic in
`web/layouts/base.templ` + `web/static/js/theme.js`.

- daisyUI is configured with three themes
  (`web/static/css/input.css`):
  `themes: light --default, dark --prefersdark, silk;`
- The rendered `<html>` is hardcoded to the dark theme:
  `data-theme="dark"` in `web/layouts/base.templ` and `web/pages/login.templ`.
  `web/static/js/theme.js` then re-applies a stored value from `localStorage`
  (default `'dark'`). The in-app `ThemeToggle` (`web/components/sidebar.templ`)
  only ever switches between `dark` and `silk`.

Semantic tokens in use (all from daisyUI's theme variables, referenced as
Tailwind utilities): `base-100`, `base-200`, `base-300`, `base-content`,
`primary`, `secondary`, `accent`, `neutral`, `info`, `success`, `warning`,
`error`, plus their `-content` pairs (e.g. `text-error-content` in
`web/components/dashboard_grid.templ`).

Role assignments observed in the code:

| Role                  | Token                                      | Evidence |
|-----------------------|--------------------------------------------|----------|
| App background        | `bg-base-100`                              | `body` in `web/layouts/base.templ` |
| Panel / sidebar / card surface | `bg-base-200`                     | `web/components/sidebar.templ`, most cards |
| Nested / muted surface | `bg-base-300`, `bg-base-200/50`, `bg-base-300/50` | `web/pages/login.templ` body, `web/components/image_zoom.templ` |
| Body text             | `text-base-content`                        | `body` in `web/layouts/base.templ` |
| De-emphasised text    | `opacity-50` / `opacity-70` / `text-base-content/XX` | pervasive, e.g. `web/components/page_header.templ` |
| Primary action        | `btn-primary`                              | 51 uses across `web/pages` + `web/components` |
| Destructive action    | `btn-error` (15 uses) / `text-error`       | `web/components/confirm_modal.templ` |
| Stock / bin status    | `bg-success`, `bg-warning`, `bg-error`; empty = `bg-base-200` | `web/components/dashboard_grid.templ` (`GetClass`) |

Status semantics (`web/components/dashboard_grid.templ`,
`web/components/dashboard_wall.templ`):

- `ok` → `bg-success text-success-content`
- `low` → `bg-warning text-warning-content`
- `critical` → `bg-error text-error-content`
- empty → `bg-base-200`
- Multi-status bins render as an inline `linear-gradient` / `conic-gradient`
  built from the CSS variables `var(--color-success)`, `var(--color-warning)`,
  `var(--color-error)`.

Custom token defined with the Tailwind v4 `@theme` block
(`web/static/css/input.css`):

```css
@theme {
  --color-base-contrast: color-mix(in oklch,
      var(--color-base-content) 10%,
      var(--color-base-200));
}
```

It is used only for the inline parts search dropdown
(`web/pages/parts.templ`: `bg-base-contrast p-3 border border-base-contrast`).

Runtime CSS variables injected in `web/layouts/base.templ` inside a
`<style id="dynamic-colors">` block, on `:root`:

```
--color-locate: #0000FF;
--color-stock-ok: #00FF00;
--color-stock-low: #FFFF00;
--color-stock-critical: #FF0000;
--color-error: #FF0000;
```

Note that `--color-error` here overrides daisyUI's `--color-error`. These raw
sRGB values are an existing part of the system (used by hardware / LED
locate + status rendering); they are not the CLI swatch palette.

The canonical brand asset is `web/static/img/wledger-name-rainbow.png`, used in
the sidebar header and the mobile top bar.

---

## 2. Typography

- Base family: `font-sans` is set on `<body>` in `web/layouts/base.templ`
  (`class="bg-base-100 text-base-content font-sans"`). No custom font is
  loaded; daisyUI/Tailwind defaults apply.
- Monospace: `font-mono` (55 occurrences) is used deliberately for
  machine-readable values — IDs, role label, timestamps, and status/key badges.
  Examples: sidebar role/guest label (`web/components/sidebar.templ`),
  `ID: {…}` and `badge … font-mono` in `web/pages/audit_logs.templ`,
  `text-xs font-mono opacity-40` in `web/pages/parts.templ`.

Size ladder actually used (counts across `web/pages` + `web/components`):

| Class       | Uses | Typical role |
|-------------|------|--------------|
| `text-[10px]` | 13 | micro metadata (e.g. `web/components/bin_picker_card.templ`, `bin_picker_grid.templ`) |
| `text-xs`   | 85   | metadata, labels, captions |
| `text-sm`   | 130  | dominant body / secondary text |
| `text-base` | 23   | body and menu labels |
| `text-lg`   | 12   | modal titles (`web/components/modal.templ`) |
| `text-2xl`  | 10   | card / empty-state headings |
| `text-3xl`  | 7    | page titles (mobile) |
| `text-4xl`  | 2    | page titles (desktop, `md:text-4xl`) |
| `text-5xl`  | 1    | large glyph, `web/pages/force_reset.templ` |
| `text-9xl`  | 1    | HTTP code display (`web/pages/error.templ`) |

Weights: `font-bold` dominates (134 uses), with `font-semibold`, `font-black`
(stat values, `web/components/stats_card.templ`), and `font-normal` for
sidebar buttons.

Conventions:

- **Section / metadata labels** are `text-xs`–`text-sm`, `uppercase`,
  plus `tracking-wider`/`tracking-widest`, and usually `font-bold` with
  `opacity-50`–`opacity-80`. Examples:
  `web/components/stats_card.templ`, `web/pages/dashboard.templ`,
  `web/components/dashboard_grid.templ`.
- **Page title** comes from `PageHeader` (section 5): `text-3xl md:text-4xl
  font-bold`, description `text-sm md:text-lg opacity-70 mt-2`.
- **Machine values** (IDs, counts keyed to the backend) are `font-mono` with
  reduced opacity.

---

## 3. Spacing, density & layout

- Page shell (`web/layouts/base.templ`): fixed sidebar `w-80`, mobile header
  `h-16`, and main content padding `p-4 md:p-8` with
  `flex-1 overflow-y-auto`. Content scrolls inside `#main-content`, not the
  page — HTMX swaps target `#main-content`.
- Card padding is one of `p-4`, `p-5`, `p-6` (`card-body`); `p-6` is the most
  common for detail/section cards, `p-4` for dense cards (e.g. filters in
  `web/pages/audit_logs.templ`).
- Gap scale: `gap-2` is the workhorse (117 uses), then `gap-4`, `gap-6`;
  vertical rhythm uses `space-y-4` / `space-y-6` / `space-y-8` (e.g.
  `web/pages/dashboard.templ` uses `space-y-12` between walls).
- Grid patterns:
  - Inventory cards: `grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3
    xl:grid-cols-4 gap-6` (`web/pages/parts.templ`).
  - Audit filter form: `grid-cols-1 md:grid-cols-3 lg:grid-cols-6 gap-4`.
  - Dashboard stat row: `flex flex-col md:flex-row md:flex-wrap justify-center
    gap-6` (`web/pages/dashboard.templ`).

**Density:** the UI is medium-density. Cards carry `shadow-xl` + `border`
rather than padding-heavy spacing; metadata is compressed with
`text-xs opacity-50`. There is no compact/comfortable mode toggle.

**Content width:** pages choose their own max width inside the padded main
area. Observed wrappers: `max-w-7xl` (`web/pages/audit_logs.templ`,
`web/pages/low_stock.templ`), `max-w-6xl` (`web/pages/product_labels.templ`),
`max-w-5xl` (`web/pages/part_detail.templ`, `web/pages/drawer.templ`),
`max-w-4xl` (`web/pages/settings.templ`, `web/pages/part_create.templ`,
`web/pages/part_edit.templ`, `web/pages/suppliers.templ`), and several pages
with **no** wrapper (relying on `main` padding alone). This spread is recorded
as debt in section 8.

---

## 4. Surfaces, borders & elevation

- **Card (dominant primitive):** `class="card bg-base-200 shadow-xl border
  border-base-300"` with `card-body`. This exact combination appears in
  `web/components/empty_state.templ`, `web/components/stats_card.templ`,
  `web/components/dashboard_grid.templ`, `web/pages/audit_logs.templ`,
  `web/pages/dashboard.templ`. Variants: `bg-base-100` for cards that sit on
  a `bg-base-200` region or above content (e.g. login card, image-zoom box),
  `bg-base-200/50` for nested inset panels.
- **Elevation scale:** `shadow-xl` for cards; `shadow-sm` for inputs/buttons
  and dense cards; `shadow-md`/`shadow-lg` for hover or intermediate surfaces;
  `shadow-2xl` on card hover (`group-hover`, `hover:shadow-2xl`); `shadow-inner`
  for inset grids (`web/components/bin_picker_card.templ`,
  `web/components/dashboard_wall.templ`).
- **Borders:** `border-base-300` is the standard 1px divider/surface border
  (122 `border-base*` uses). Shared regions are separated with
  `border-t`/`border-b`/`border-r` + `border-base-300`
  (`web/layouts/base.templ`, `web/components/sidebar.templ`). Softer dividers
  use opacity suffixes: `border-base-300/50`, `border-base-content/5`.
  Accent borders use `border-primary`, `border-primary/30`,
  `border-primary/50`.
- **Radius:** `rounded-lg` is the default (25 uses), `rounded-xl` for larger
  inset panels and image frames, `rounded-full` for pills (search input, status
  legend, badges), `rounded-md`/`rounded` for small tiles, and daisyUI's
  `rounded-box` for dropdown panels. daisyUI's `--rounded-btn` variable is
  relied on for button radius (`web/components/barcode_scanner.templ` uses
  `var(--rounded-btn, 0.5rem)`).
- **Empty placeholders:** dashed border block,
  `border border-dashed border-base-300 rounded-lg` with `opacity-50`
  (`web/components/dashboard_grid.templ`). The richer `EmptyState` component
  (section 5) uses a full card with a `grayscale` icon and `opacity-60`.

---

## 5. Reusable Templ primitives

These are the shared, importable building blocks. **New pages must compose
these rather than re-implement their markup.** All are in `web/components`
(except the layout shell).

| Primitive | Signature / file | Purpose & key classes |
|-----------|------------------|-----------------------|
| `Base` layout | `web/layouts/base.templ` → `Base(title string, user auth.User)` | Full HTML shell: sidebar drawer, mobile header, `#main-content`, toast container, script/style includes. Wrap every page body with `@layouts.Base(...)`. |
| `Sidebar` | `web/components/sidebar.templ` → `Sidebar(user auth.User)` | Fixed `w-80` nav column. Composed of `NavLinks`, `CommunityLinks`, `LanguageSwitcher`, `ThemeToggle`, `LogoutButton`/`LoginButton`, `GlobalOffButton`. |
| `PageHeader` | `web/components/page_header.templ` → `PageHeader(title, description string, breadcrumbs []Breadcrumb)` | Standard page title block: optional breadcrumbs, `text-3xl md:text-4xl font-bold` title, `text-sm md:text-lg opacity-70` description, right-aligned action slot (children). Responsive `flex-col md:flex-row`. |
| `EmptyState` | `web/components/empty_state.templ` → `EmptyState(title, description, icon string)` | Full-width empty card, centred `text-6xl grayscale` icon, title `text-2xl font-bold`, children for actions. |
| `PhysicalStorageEmptyState` | `web/components/physical_storage_empty.templ` → `PhysicalStorageEmptyState(title, description)` | Physical-storage empty card using the same surface tokens and the existing box SVG icon, without emoji glyphs. |
| `Modal` | `web/components/modal.templ` → `Modal(props ModalProps)` | daisyUI `<dialog class="modal">` with `modal-box bg-base-100`, top-right `btn btn-sm btn-circle btn-ghost` close, `modal-backdrop`. `BoxClass`/`TitleClass` allow size/colour variants. |
| `DeleteButton` | `web/components/confirm_modal.templ` → `DeleteButton(props DeleteButtonProps)` | `btn btn-outline btn-error` trigger plus a confirm `Modal` with `btn-error` submit. |
| `Toast` | `web/components/toast.templ` → `Toast(props ToastProps)` | `alert shadow-lg` variants `ToastInfo/Success/Warning/Error`; Alpine auto-remove after 5 s. |
| `StatCard` | `web/components/stats_card.templ` → `StatCard(title, value, textColor string)` | Dashboard stat tile: `card bg-base-200 shadow-xl border border-base-300`, uppercase label, `text-3xl font-black` value. |
| `PartSearchBar` | `web/components/part_search_bar.templ` → `PartSearchBar(search string, canWrite bool, binID *int64)` | HTMX search input (`join`, `rounded-full`) + scan button + Import/Add-Part buttons. |
| `DashboardGrid` / `DashboardContainerCard` / `BinPickerGrid` / `BinPickerCard` | `web/components/dashboard_grid.templ`, `dashboard_wall.templ`, `bin_picker_grid.templ`, `bin_picker_card.templ` | Bin-grid renderers; share the `bin.GetClass()`/`GetStyle()` status visuals. |
| `StorageBinPicker` | `web/components/storage_bin_picker.templ` → `StorageBinPicker(view)` | Physical cabinet/drawer selection grouped by explicit attachments, with a separate unplaced group and the stable `bin_id` event contract. |
| `ImageZoomModal` | `web/components/image_zoom.templ` → `ImageZoomModal(id int64, name, imagePath string)` | Full-size image `<dialog>` with `modal-box bg-base-100 max-w-4xl`. |
| `BarcodeScannerModal` / `BarcodeTrigger` | `web/components/barcode_scanner.templ` | Global scanner dialog + trigger; opened via `startGlobalScanner(targetID)`. |
| `GridPainter` | `web/components/grid_painter.templ` | Hardware bin-mapping editor. |
| `TagInput` | `web/components/tag_input.templ` | Comma/Enter tag entry with `label-text-alt` hint. |
| `LocateButton` / `PartLocateButton` | `web/components/locate_button.templ`, `part_locate_button.templ` | `btn btn-ghost text-accent` locate actions. |
| `QuestionTooltip` | `web/components/tooltips.templ` → `QuestionTooltip(text string)` | `tooltip` with responsive placement (`tooltip-bottom md:tooltip-right`) and `before:` width overrides. |
| `OrphanedStockWarning` | `web/components/orphaned_stock_warning.templ` | `tooltip tooltip-warning` on a warning triangle. |
| `ImportModal` / `ImportResult` | `web/components/import_modal.templ` | CSV bulk import dialog + result list. |
| `BackupRestore` | `web/components/backup_restore.templ` | Backup/restore card. |
| `DymoLabelPrint` | `web/components/dymo_label_print.templ` | Label printing layout. |
| `ColorPicker` | `web/components/color_picker.templ` | LED colour input. |
| `ChangePasswordForm` | `web/components/password_change.templ` | Password form fragment. |
| `PartFavoriteButton` | `web/components/part_favorite_button.templ` | `btn btn-ghost btn-circle btn-sm`, `text-warning` star. |
| `MoveStockModal` | `web/components/move_part_modal.templ` | Move-stock dialog. |
| `PartForm` / `BinOptions` | `web/components/part_form.templ` | Shared create/edit part form. |
| `AuditDetailModal` | `web/components/audit_detail_modal.templ` | Old/new value diff dialog. |
| Icons | `web/icons/*.templ` | Line SVGs, typically used at `w-5 h-5` (nav) or `h-4 w-4`/`h-6 w-6` (inline). |

Button idiom (daisyUI): `btn` + a variant (`btn-primary`, `btn-ghost`,
`btn-outline`, `btn-error`, …) + optional shape (`btn-square`, `btn-circle`,
`btn-block`) + size (`btn-xs`, `btn-sm`, `btn-md`). Sidebar/toolbar buttons
that should read as menu rows add `justify-start w-full normal-case
text-base font-normal px-2` (`web/components/sidebar.templ`).

Form idiom: `form-control` → `label`/`label-text` → `input input-bordered` /
`select select-bordered` / `textarea`, as in `web/pages/login.templ` and
`web/pages/audit_logs.templ`. Inputs paired in a row use daisyUI `join` +
`join-item` (`web/components/part_search_bar.templ`).

---

## 6. Interaction, focus & motion states

- **Focus:** most controls rely on daisyUI's built-in `:focus-visible`/active
  styles on `btn`, `input`, `select`, `checkbox`, etc. The sidebar search can be
  focused from anywhere with `Ctrl/Cmd+K` via Alpine
  (`@keydown.window.meta.k.prevent="$el.focus()"`, `web/components/sidebar.templ`).
  The legacy `BinPickerCard` uses a selected-state ring
  (`ring-4 ring-primary border-primary z-20 scale-110`); the M6
  `StorageBinPicker` uses a selected wash plus check glyph and a 2px
  focus-visible outline for keyboard navigation. The outline is explicit
  because daisyUI's `btn` box-shadow prevents the Tailwind ring utilities from
  rendering their ring on this button.
- **Hover:** cards lift via `hover:shadow-2xl` / `hover:shadow-lg transition`
  (`web/pages/parts.templ`, `web/components/inspiration_form.templ`); bins
  `hover:scale-110 hover:shadow-md hover:z-10`
  (`web/components/dashboard_grid.templ`); image overlay buttons reveal with
  `opacity-0 group-hover:opacity-100 transition-opacity`
  (`web/pages/parts.templ`); links use `hover:opacity-80 transition-opacity`.
- **HTMX loading states** (defined in `web/layouts/base.templ`):
  `.htmx-indicator` hidden by default and shown during `.htmx-request`;
  button contents other than the indicator fade to `opacity: 0` and the button
  becomes `pointer-events: none`; non-button `[hx-get]`/`[hx-post]` targets
  drop to `opacity: 0.5` and block pointer events.
- **Alpine patterns:** `x-cloak` is hidden via a global rule
  (`[x-cloak] { display: none !important; }`); toasts auto-remove after 5 s
  (`web/components/toast.templ`), and the theme swap animates for ~500 ms
  (`web/static/js/theme.js`).
- **Toasts/alerts:** transient feedback is an `alert` (`alert-info|success|
  warning|error`) appended into the top-right `#toast-container`
  (`toast toast-top toast-end z-[100]`).
- **Drag & drop** (SortableJS) states are styled globally in
  `web/layouts/base.templ`: `.sortable-ghost` (dashed primary outline),
  `.sortable-chosen`, `.sortable-drag` (lifted shadow, `cursor: grabbing`).
- **Scrollbars:** `.custom-scrollbar` gives a 4px thumb derived from
  `oklch(var(--bc) / 0.2)` (used by the sidebar menu list).
- **Motion:** transitions are short and utility-level —
  `transition-all duration-200/300`, `transition-opacity`,
  `transition-colors`. There is no motion system or animation library.

---

## 7. Responsive behavior

- Shell: `drawer md:drawer-open` (`web/layouts/base.templ`). At `md` and up the
  sidebar is persistent; below `md` it collapses behind a hamburger and the
  `md:hidden h-16` top bar shows the logo. The `drawer-overlay` closes it.
- Header/actions: `PageHeader` stacks on mobile and goes to a row at `md`
  (`flex-col md:flex-row items-start md:items-center`).
- Grids step at `md`/`lg`/`xl` (see section 3).
- Tables are always wrapped in `overflow-x-auto`
  (`web/pages/audit_logs.templ`, `web/components/dashboard_grid.templ`).
- Tooltips reposition by breakpoint (`tooltip-bottom md:tooltip-right`,
  `tooltip-top lg:tooltip-left`, `web/components/tooltips.templ`,
  `web/components/grid_painter.templ`).
- Form/toolbar rows use the `flex-col … md:flex-row` / `sm:` pattern; buttons
  commonly pair `w-full md:w-auto` with `flex-1 md:flex-none`.
- Sidebar breakpoint helpers seen: `sm:w-auto`, `md:w-auto`, `sm:flex-row`,
  `lg:grid-cols-*`, `xl:grid-cols-4`.

---

## 8. Known inconsistencies & accepted debt

These are recorded as evidence of the current state, not proposals. Do not
"fix" them opportunistically inside unrelated work; do not build new pages on
top of the broken variants.

1. **Registered-but-unreachable `light` theme.** `input.css` registers
   `light --default`, but `base.templ`/`login.templ` hardcode
   `data-theme="dark"` and the `ThemeToggle` only toggles `dark ⇄ silk`. The
   default theme is effectively dead through the UI.
2. **Invalid utility classes that silently no-op.** `text-md`
   (`web/pages/audit_logs.templ` ×4, `web/pages/dashboard.templ`) and `btn-s`
   (`web/components/part_form.templ` ×2) are not valid Tailwind/daisyUI classes
   and produce no styling. Use `text-base`/`btn-sm` instead in new code.
3. **Hardcoded palette leaks outside the token system.** Raw values appear in
   shipped templates: `text-[#ff5e5b]` (Ko-fi icon, `web/components/sidebar.templ`),
   and `text-gray-500`/`text-gray-400`/`bg-white`/`text-black`/`text-white`
   across `web/pages/product_labels.templ`, `suppliers.templ`,
   `inspiration.templ`, `bin_labels.templ` and
   `web/components/inspiration_form.templ`, `dymo_label_print.templ`.
4. **Raw sRGB status variables vs daisyUI oklch tokens.** The injected
   `:root` block in `base.templ` defines `--color-stock-*` as plain hex and
   reassigns `--color-error`; these coexist with daisyUI's semantic tokens and
   are not the same palette.
5. **No single page-width convention.** Wrappers range from `max-w-7xl` down to
   `max-w-2xl`, and several pages use none at all (section 3). New pages should
   pick a width deliberately rather than assume one.
6. **Card surface & elevation drift.** Cards alternate `bg-base-200` /
   `bg-base-100` / `bg-base-200/50` and mix `shadow-sm`/`shadow-md`/`shadow-xl`/
   `shadow-2xl` without a documented rule. The dominant card is
   `card bg-base-200 shadow-xl border border-base-300`.
7. **Duplicated bin rendering.** `DashboardGrid` (`dashboard_grid.templ`),
   `DashboardContainerCard` (`dashboard_wall.templ`), `BinPickerGrid` and
   `BinPickerCard` render the same bin grids with small divergences
   (`rounded-md` vs `rounded-lg`, `gap-1.5` vs `gap-2`, `bg-base-100` vs
   `bg-base-200/50`, legend dot sizes `w-2` vs `w-2.5`). Reuse the existing
   component that matches the context. `StorageBinPicker` is a separate M6
   primitive because its tree is grouped by physical cabinet/drawer attachments,
   not by the legacy electrical grid; it preserves the stable `bin_id` selection
   contract without reusing container-based placement.
8. **Duplicated toast container / alert patterns.** `#toast-container` is
   declared in `base.templ`, re-declared as an `hx-swap-oob` fragment in
   `toast.templ`, and recreated by `web/static/js/toast.js`; `ErrorPartial`
   (`web/pages/error.templ`) reimplements an alert instead of using `Toast`.
9. **Login bypasses the layout.** `web/pages/login.templ` hand-writes its own
   `<html>`/`<head>` instead of using `layouts.Base`, duplicating the shell
   concerns.
10. **Button size mixing.** `btn-xs`/`btn-sm`/`btn-md` are used inconsistently
    for comparable actions; there is no encoded rule for which size a given
    context uses.

---

## Usage contract for new (M6) pages

1. Wrap the page body in `@layouts.Base(title, user)` and render
   `@components.PageHeader(...)` for the title block.
2. Compose existing primitives from section 5 (cards, modals, toasts,
   empty states, search bars, icon set) — do not fork their markup.
3. Use daisyUI semantic tokens (`base-*`, `primary`, `success`, `warning`,
   `error`, …) and the utility cluster catalogued in sections 2–4
   (`card bg-base-200 shadow-xl border border-base-300`, `text-xs/sm/…`,
   `font-mono` for machine values, `gap-2`-based spacing, `rounded-lg`).
4. Respect the responsive rules in section 7 (drawer at `md`, stacked→row
   headers, `overflow-x-auto` tables, breakpoint tooltips).
5. Do not introduce new color tokens, fonts, radii, shadows, component
   libraries, or a second layout shell. If a genuinely new primitive is
   required, build it in `web/components` following the dominant patterns here
   so it stays reusable.
