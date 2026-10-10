# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 38
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 026 — Dashboard Completeness and Readability
Production-Authorization: NO_PRODUCTION_CHANGES
Base-Commit: 7b5f63a11747310752aa2a186964d1970d36585f

## Objective
Fix source-confirmed dashboard omission of controllers without grid-mapped bins and prevent long bin/container names overflowing tiny tiles, while preserving the current one-controller/two-string/68-bin production configuration. Do not invent extra controllers or cabinets. This is development only. User requests implementation-ready handoffs with concrete code guidance and tests, not just abstract objectives.

## Verified source map
- sql/queries/dashboard.sql: GetDashboardGrid and GetDashboardGridByController start FROM bins and INNER JOIN containers/controllers, filter mapped grid_x/y. Controllers with no mapped bins disappear.
- internal/dashboard/service.go: newDashboardViewModel and newDashboardViewModelFromRows assume non-null row.ContainerID and row.BinID; blindly create containers and bins. Any LEFT JOIN change must update generated sqlc types and guard nullable IDs.
- web/components/dashboard_grid.templ: legacy tiles have w-12 h-12 text-xs and unbounded <span>{ bin.Name }</span>; container header has flex justify-between without min-w-0/truncate.
- web/components/dashboard_wall.templ: modal tiles same 48px unbounded text, wall card titles and modal header can overflow; class border-500/10 invalid (optional fix if touched).
- web/pages/dashboard.templ: when walls exist it displays wall cards; otherwise legacy controllers. Test both branches. Current deployed DB has 0 walls, 1 controller, 2 strings, 68 mapped bins.
- sqlc generated code and templ generated code are committed; regenerate deterministically.

## Recommended implementation (adapt to actual schema/sqlc, NOT blind paste)
A. Query all controllers and optional containers/bins, preserving mapped bins only, but retain controller and container rows without mapped bins. One candidate SQL pattern:

-- name: GetDashboardGrid :many
SELECT c.id AS controller_id, c.name AS controller_name, c.is_online,
       cont.id AS container_id, cont.name AS container_name,
       cont.segment_id, b.id AS bin_id, b.name AS bin_name,
       b.grid_x, b.grid_y, p.id AS part_id, pa.quantity,
       p.min_stock_threshold, p.reorder_level
FROM controllers c
LEFT JOIN containers cont ON cont.controller_id = c.id
LEFT JOIN bins b ON b.container_id = cont.id
    AND b.grid_x IS NOT NULL AND b.grid_y IS NOT NULL
LEFT JOIN part_assignments pa ON pa.bin_id = b.id
LEFT JOIN parts p ON p.id = pa.part_id
ORDER BY c.name, cont.segment_id, cont.name, b.grid_y, b.grid_x;

Use equivalent WHERE c.id = ? for GetDashboardGridByController. Verify sqlc nullable field types (e.g. sql.NullInt64 for optional container/bin IDs) after generation; don't assume exact types. Guard before creating a container or bin:

if !row.ContainerID.Valid { continue }  // if generated as sql.NullInt64
...
if !row.BinID.Valid { continue }

These are ILLUSTRATIVE; use actual generated types and row fields. Ensure a controller with no containers renders existing DashboardGrid empty state; a controller with an empty container shows the container but no bogus bin ID 0. Preserve ordering, status aggregation and part joins. If sqlc generated models complicate this, a separate GetDashboardControllers query and composition is acceptable; explain tradeoff.

B. In BOTH dashboard_grid.templ and dashboard_wall.templ, make tile text bounded while retaining complete names via title attribute and keyboard-accessible link. Example to adapt:

<a ... class={ "w-12 h-12 min-w-0 overflow-hidden ... " + bin.GetClass() } title={ bin.Name } aria-label={ bin.Name }>
  <span class="block max-w-full px-1 truncate text-center leading-tight">{ bin.Name }</span>
</a>

Consider 56px tiles if appropriate, but preserve accurate grid positions and horizontal scrolling. Prevent long container/controller names from pushing badges/IDs offscreen, e.g. header class="flex min-w-0 items-center gap-2", title class="min-w-0 flex-1 truncate" title={container.Name}, ID class="shrink-0". For wall modal headers, allow wrapping/stacking on narrow screens. Avoid hover transforms causing adjacent labels to overlap. Verify readability visually if a local authenticated dev browser is available; do NOT access production browser or LEDs.

C. Tests: DB integration/service tests for controller with no containers; controller with container but no bins; unmapped bins only; mixed mapped/unmapped; multi-part statuses and stable sort; GetGridByController returns controller even if empty and ErrNoRows for nonexistent ID. Template render tests for long names and title/aria-label, bounded classes, wall modal and legacy path. Test against temporary SQLite only. Preserve existing dashboard and security tests.

D. Generate sqlc/templ outputs per repo toolchain; inspect generated diff. Run gofmt only changed Go files, go build ./..., go vet -tags fts5 ./..., go test -tags fts5 -count=1 ./..., and targeted race checks. Report exact SHAs and risks. No broad 44-file formatting or CI changes (future Task 027).

## Checkpoint and Git workflow
Read AGENTS.md and docs. Start a dedicated branch from latest main 7b5f63a. Commit and push implementation branch for independent review. Do NOT merge to main. Publish Sequence 39 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 026 with branch, commit SHA, parent, changed files, tests, screenshot/visual verification status, known limitations, and recommended follow-up. Commit/push only AI_HANDOFF.md on experiment/ai-handoff; no force. STOP.

## Boundaries
No production deployment/restart, database writes/migrations on production, backup/restore, physical WLED commands, Home Assistant changes, secrets or proxy changes. Existing production release 7b5f63a remains unchanged. Authenticated production browser verification of Task 025 is separately pending; do not assume it was done.
