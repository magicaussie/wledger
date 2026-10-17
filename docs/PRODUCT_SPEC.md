# WLEDger — Authoritative Target Product Specification

Status: **OWNER-APPROVED TARGET REQUIREMENTS; NOT A CLAIM OF IMPLEMENTATION**.
Updated: 2026-10-17. Scope: requirements agreed in owner design discussion.
This document supersedes contradictory design assertions in `ROADMAP.md`, `docs/storage-and-scanning.md`, `docs/roadmap.md`, and historical handovers. Actual deployed and committed behavior must be verified against source and tests. Do not modify application code based solely on this document without a separate implementation authorization.

## Foundational model
- Cabinet, WLED controller, physical string/output, drawer, bin, and part are distinct entities with stable internal IDs. Renaming, renumbering, repositioning, and IP changes do not change IDs or stock associations.
- Each cabinet has one or more dedicated controllers; a controller belongs to at most one cabinet. Controllers have up to **five physical strings**, corresponding to actual configured WLED GPIO outputs, not arbitrary WLED segments.
- A cabinet's manually ordered controllers and strings create one **continuous 1-based cabinet pixel namespace**. Each cabinet restarts at 1. Underlying physical LED identity is controller ID + string ID + string-local offset.
- **Each drawer has exactly one continuous cabinet-wide start/end pixel range**, possibly crossing controller/string boundaries. This replaces older proposals for multiple disjoint ranges per drawer. Bins have **no independently assigned pixels**; all bin/item locating illuminates the parent drawer.
- Drawer numbers are manually assigned and unique within a cabinet. Bin numbers are manually assigned and unique within their drawer. Physical wiring direction is independent of drawer numbering. Unassigned pixels are permitted; drawer pixel overlap is not.
- Example: two 100-pixel horizontal rows, first 1–100 left-to-right, second 101–200 right-to-left. Ten equal drawers each row: first row drawer 1 = 1–10; second row leftmost drawer 11 = 191–200. Never infer mappings from visual order.

## Hardware and lighting
- Hardware page: add/edit controller name, IP, port; Check Connection, View WLED, Strings, Delete. Delete blocked until assignments safely resolved; never alter physical WLED config on controller deletion.
- Strings page: add/edit/reorder/delete up to five physical strings; choose output/GPIO and pixel count; Test and Off. Test modes red/green/blue/white, flash red, chase at three speeds; use safe brightness and bounded time.
- Support read-only WLED output discovery/import and optional explicitly confirmed configuration push; offline draft configurations and mismatch resolution must be supported. Never silently overwrite device settings.
- Automatic/manual connectivity monitoring with globally configurable interval. Restart and genuine reconnection request LED Off where authorized and safely scoped; never claim success without confirmation or alter unrelated lighting.
- Cabinet Pixels On/Off covers all pixels of its controllers, including unassigned pixels. Drawer-page Master Off covers **only drawer-assigned pixels** in that cabinet.
- Multiple drawers can be lit simultaneously. Manual drawer toggle persists until manually turned off; Locate temporarily overrides colour, restores manual state at timeout, and supports simultaneous or sequential multi-location navigation. Globally configurable colours, cabinet test colour, brightness, and Locate timeout.
- Editing string lengths, order, or cabinet membership must preserve existing physical LED assignments where possible; show a before/after preview and block or require explicit resolution for conflicts.
- Physical writes remain **disabled until explicit owner authorization**. Preview, offline configuration and tests must not bypass this restriction.

## Layout designers
- Reuse/simplify existing cabinet/bin drawing tools. Cabinet Layout defines wiring orientation, horizontal rows/vertical columns, start/end cabinet pixel range and direction per row/column, with optional pixel/direction overlay.
- Distinguish decorative shapes from real Add Drawer / Add Bin objects. Support rectangles, irregular polygons, dragging/resizing/rotation/colour, layers, grouping, duplicate, multi-select, align/distribute, grid snapping, zoom/pan, optional millimetre dimensions, undo/redo.
- Drawer editor: permanent ID, manually assigned number, optional name, single start/end pixel range, Bin Layout, Test Pixels and Off using unsaved draft range. Scoped tests must stop on cancel/navigation or server timeout without cancelling unrelated operations.
- Each drawer has its own independent bin layout. Bins have stable IDs, manual numbers, optional names/dimensions, **no pixels**.
- Save/Cancel with unsaved warning, validation and transactional saves. Duplicates get fresh IDs, no copied stock or live LED assignment. Cabinet duplication copies layout/drawers/bin structure but not controllers, pixel mappings or inventory. No separate template system.
- Block destructive deletion of occupied bins/drawers/cabinets until inventory and hardware dependencies are resolved.

## Inventory, scanning, labels and files
- A part can occupy multiple bins across cabinets, with quantities per location, full stock movement history, atomic transfers and strict nonnegative on-hand stock.
- Part details show total and per-location quantities with Locate/Transfer. Stock reservations per bin track on-hand, reserved and available; picking/transfers respect reservations.
- Context-sensitive QR/barcode scanning: default opens entity; receiving, picking, transfer and adjustment modes select the relevant entity. QR-assisted transfer scans source bin, part, quantity, destination bin, confirmation. Bin QR scan may auto-Locate according to global setting.
- Internal stable QR identities for cabinet/drawer/bin/part; parts also support multiple external manufacturer/supplier barcodes. Unknown barcode offers inventory search, associate to existing part, web search, or reviewed part creation.
- Labels: reusable designer with text, shapes, images, QR, Code128, EAN/UPC, variable fields, physical sizes/orientation, preview, batch printing, Brother/DYMO support, direct printer paths where compatible and PDF fallback. Bin labels show human-readable cabinet/drawer/bin.
- Product identification from browser-connected or server-connected USB webcam/microscope, multiple captures, OCR, markings and web/datasheet matching. Multiple local/cloud AI providers/models, task-specific defaults, hybrid fallback, privacy controls, source/confidence display and explicit approval before changes.
- Fixed shared product specification fields, plus general notes; no dynamic category-specific field schema. Multiple product images and documents. Store binary files in managed server directories with SQLite metadata; allow shared files referenced by many parts, safe reference-aware deletion and complete backups of DB + files.

## Projects, purchasing and AI
- Lightweight Projects/Jobs: job details, BOM, requirements, stock reservation, picking, consumption, returns and shortages.
- Manual and CSV/Excel/CAD BOM import with AI-assisted matching; never silently substitute an alternative part.
- Supplier directory, purchasing, POs, partial receiving, web price/availability/lead-time comparison and configurable supplier API integrations; manual/web fallback. Never place an external order without explicit approval.
- Existing supplier, stock, scanning, DYMO and WLED implementations are candidates for reuse, not proof that these expanded requirements are complete.

## Migration, verification and scope
- Preserve production data, authentication, stock quantities, uploads, stable IDs and legacy LED configuration. Do not silently reinterpret old controller-local, segment-relative or bin LED indexes as cabinet-wide drawer mappings.
- Inventory current schema, routes, tests, and live deployment version before designing migrations. Distinguish **implemented**, **partial**, **planned**, and **unverified** in every handover.
- No code changes, DB migrations, deployments, `main` merge or physical WLED output are authorized by this documentation cleanup.
- Priority: complete documentation reconciliation first; implementation phase order remains to be confirmed. Unresolved decisions: initial purchasing ordering scope, AI optionality and final implementation phasing. Do not infer owner answers.
