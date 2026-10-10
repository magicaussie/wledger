# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 39
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 026 — Dashboard Completeness and Readability
Production-Authorization: NO_PRODUCTION_CHANGES
Base-Commit: 7b5f63a11747310752aa2a186964d1970d36585f
Implementation-Branch: fix/dashboard-completeness-and-readability
Implementation-Commit: 696475c8d58786f3e0e39c6b9e38e1a412f1f7cb
Result: IMPLEMENTED ON BRANCH — NOT MERGED, NOT DEPLOYED

## Summary
Implemented Task 026 on a dedicated branch from latest main (`7b5f63a`).
Controllers with no grid-mapped bins now appear on the dashboard, and long
bin/container/controller names are bounded. No merge to main, no deployment.

## Changed files (7b5f63a..696475c)
- `sql/queries/dashboard.sql`
- `internal/db/dashboard.sql.go` (regenerated)
- `internal/db/querier.go` (regenerated doc comments)
- `internal/dashboard/service.go`
- `internal/dashboard/service_test.go`
- `web/components/dashboard_grid.templ` (+ `_templ.go`)
- `web/components/dashboard_wall.templ` (+ `_templ.go`)
- `web/components/dashboard_render_test.go` (new)

## What changed
A. **SQL** — `GetDashboardGrid` and `GetDashboardGridByController` now start
   `FROM controllers` and `LEFT JOIN containers`, then `LEFT JOIN bins` with the
   `grid_x/grid_y` filter in the `ON` clause (not `WHERE`). A controller with no
   containers, and a container with no grid-mapped bins, now yield a row with a
   NULL container/bin instead of disappearing. Ordering, part joins and status
   aggregation are unchanged.
B. **sqlc regenerated** — `ContainerID`/`BinID` are now `sql.NullInt64`,
   `ContainerName`/`BinName` `sql.NullString`, `SegmentID` `sql.NullInt64`. The
   service guards `!row.ContainerID.Valid` / `!row.BinID.Valid`, keeping the
   controller/container while skipping the missing child.
   `GetGridByController` still returns `sql.ErrNoRows` for a nonexistent
   controller, and now returns the controller (empty) when it exists with no
   mapped bins.
C. **Templ** — `dashboard_grid.templ` and `dashboard_wall.templ` tiles are
   bounded (`w-12 h-12 min-w-0 overflow-hidden` + inner
   `block max-w-full px-1 truncate text-center leading-tight`), keep the full
   name via `title` + `aria-label`, and headers use `min-w-0`/`flex-1`/`truncate`
   with `shrink-0` badges/IDs. The wall modal header wraps on narrow screens.
   Removed the invalid `border-500/10` class in `StockHealthIndicator`.

## Tests
- `internal/dashboard/service_test.go`: controller with no containers; container
  with only unmapped bins; mixed mapped/unmapped; multi-part statuses + grid
  sort; container sort by segment; `GetGridByController` empty controller;
  `GetGridByController` nonexistent → `sql.ErrNoRows`. The in-memory test DB is
  now per-test (unique shared-cache name) to avoid cross-test contamination.
- `web/components/dashboard_render_test.go` (new): long bin name keeps
  `title` + `aria-label` and `truncate`/`overflow-hidden` classes; empty
  controller renders the empty state; wall card + modal bound long names, keep
  `title` attributes, and no longer emit `border-500/10`.

## Verification (all run locally)
- `templ generate` → `updates=0` (committed generated output matches source).
- `sqlc generate` → deterministic (no further diff).
- `gofmt -l` on changed Go files → clean.
- `go build ./...` → OK.
- `go vet -tags fts5 ./...` → clean.
- `go test -tags fts5 -count=1 ./...` → all packages ok, 0 FAIL.
- `go test -race -tags fts5 -count=1 ./internal/dashboard/... ./web/components/...` → ok.

## Visual verification
- **NOT performed.** No local authenticated dev browser was available, and
  production browser/LED access is out of scope. Readability is asserted by the
  render tests (bounded classes + `title`/`aria-label`); a human visual pass on a
  local dev instance is recommended.

## Known limitations / risks
- The production configuration (1 controller, 2 strings, 68 mapped bins) is
  unchanged by this query shape: all bins are mapped, so the result set is
  identical. No production DB was read or written.
- A controller with no containers now renders the existing "No containers
  configured." empty state; a container with no mapped bins renders an empty
  grid. This is intended.
- Tile size kept at 48px (`w-12 h-12`); the guidance's optional 56px bump was not
  taken to avoid changing the established visual density.
- No broad formatting/CI changes (deferred to a future Task 027).

## Recommended follow-up
- Independent review of branch commit `696475c` against this report.
- Optional: local authenticated visual pass of the legacy dashboard and wall
  modal with long names.
- Merge to main is a separate, explicitly authorised step (not performed).

## Evidence / SHAs
- Base / main: `7b5f63a11747310752aa2a186964d1970d36585f`
- Implementation branch: `fix/dashboard-completeness-and-readability`
- Implementation commit: `696475c8d58786f3e0e39c6b9e38e1a412f1f7cb`
- Parent: `7b5f63a11747310752aa2a186964d1970d36585f`
