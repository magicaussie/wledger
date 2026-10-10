# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 27
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 021 — Investigate Missing Third Cabinet
Production-Authorization: STRICTLY_READ_ONLY (consumed)
Result: NO THIRD CABINET RECORD EXISTS (data/expectation gap, not a UI filter bug)

## 1. Domain model (VERIFIED from source at ca2789f)
- In WLEDger, **"cabinet" = a controller** (WLED device). Evidence:
  - `internal/handler/parts.go`: `CabinetScanCode(controllerID int64)`; scan code `wledger:cabinet:<id>` routes to `/hardware/<id>/grid`.
  - `internal/handler/drawers.go`: `cabinet, err := h.Queries.GetController(...)`; error text "Cabinet not found".
  - `internal/handler/hardware.go`: `HandleCabinetQR` uses `CabinetScanCode(id)`.
- The **user-facing label is "Controllers"** (i18n key `Controllers`); there is no "Cabinet" UI label. `controllers` are WLED devices; `containers` are drawers/strings (a `segment_id` within the controller); `bins` live in containers.

## 2. Production records (VERIFIED, read-only)
- `controllers` = **1 row**: id 3, name `1`, ip `192.168.1.40`, is_online 1, led_count 0, created `2026-08-17 12:20:11`.
- `containers` = **2 rows**: id 3 `String 1 (LEDs 1-628)` (controller 3, segment 0, position 0); id 4 `String 2 (LEDs 629-1141)` (controller 3, segment 1, position 1).
- `walls` = 0; `wall_cards` = 0; bins = 68 (all mapped).
- Dashboard stat query `GetDashboardStats`: `total_controllers = SELECT COUNT(*) FROM controllers` → **1** (stat renders as `online/total` = `1 / 1`).

## 3. Backups and history (VERIFIED)
- Backups `/home/spetchal/backups/wledger-pre019-20261010T050510Z` and `/home/spetchal/backups/wledger-20261010T040208Z` both contain the **same 1 controller and same 2 containers**.
- Audit history (`audit_logs`) HARDWARE events:
  - id 1 CREATE HARDWARE 1 "Added controller 1" (2026-08-15)
  - id 2 CREATE HARDWARE 2 "Imported hardware config" (2026-08-17 07:29)
  - id 3 DELETE HARDWARE 1 "Deleted controller" (2026-08-17 07:30)
  - id 4 UPDATE HARDWARE 2 "Updated LED Grid Layout"
  - id 5 DELETE HARDWARE 2 "Deleted controller" (2026-08-17 12:01)
  - id 6 CREATE HARDWARE 3 "Imported hardware config" (2026-08-17 12:20)
- So controllers 1 and 2 were created and then **explicitly deleted** by the operator; controller 3 (current) was imported. **No third controller has ever existed as a live record, and none exists in any backup.**

## 4. Dashboard/UI mechanism (VERIFIED from source)
- Dashboard body: if any **walls** exist it renders walls; otherwise it renders the "legacy controllers" grid. With 0 walls, we get the controller grid.
- `GetDashboardGrid` / `GetDashboardGridByController` use `bins JOIN containers JOIN controllers` with `WHERE b.grid_x IS NOT NULL AND b.grid_y IS NOT NULL`. Consequence: a controller that has **no bins with grid positions** would be counted by the stat but would render **no card** on the dashboard grid. (Not the cause here: the count is 1.)

## 5. Findings
- The "third cabinet" is **not present as a record** in production, and never appears in any backup. There is currently exactly **one cabinet/controller**, with **two drawers/strings**.
- Therefore this is **not** a UI filtering bug: no record is being hidden. It is a **data/expectation gap**.
- Most plausible interpretations of "two cabinets are shown … should be three":
  - (A) The user counts the two **drawers/strings** (`String 1`, `String 2`) as "cabinets" and expects a third string; or
  - (B) The user has a **third physical WLED controller/cabinet** that was never registered in WLEDger.
  - (C) A controller exists in the WLED device but only one controller is configured in WLEDger.

## 6. Unresolved / needs user input
- What does the user mean by "cabinet": a **controller** (WLED device) or a **drawer/string/segment**?
- If a controller: its real name, IP address and segment/LED layout — must not be invented.
- If a drawer/string: which controller and which segment/LED range.

## 7. Recommended least-risk remediation (no code change)
- Confirm the definition in §6 with the user.
- If a **missing controller**: register it via the existing Hardware UI — "Add Controller" (name/IP/port) or "Import Config" — which also creates its default container. Do not guess IP/name.
- If a **missing drawer/string**: add a container/segment for the existing controller via the hardware grid editor, then let the startup drawer-allocation backfill assign its allocation (`led_start/led_count`) — note this is a production data change requiring separate authorization.
- Optional, separate: the dashboard grid could be made to also list controllers with no grid-mapped bins (currently counted but cardless). This is a minor UI improvement, not the cause here, and would require separate authorization.

## Evidence / SHAs
- Production commit: `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`
- Source reviewed: same commit (`internal/handler/parts.go`, `internal/handler/drawers.go`, `internal/handler/hardware.go`, `internal/dashboard/service.go`, `sql/queries/dashboard.sql`, `web/pages/dashboard.templ`, `web/pages/hardware.templ`).
- DB sources compared: production `/home/spetchal/wledger/data/wledger.db`, `wledger-pre019-20261010T050510Z`, `wledger-20261010T040208Z`.

## Boundaries respected
STRICTLY READ ONLY: no schema/data edits, no migrations, no LED/WLED commands, no Locate, no deploy/restart, no backup/restore, no code changes, no Home Assistant changes.
