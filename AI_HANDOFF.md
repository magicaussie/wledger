# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 29
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 022 — Functional Audit and Prioritized Next Work
Production-Authorization: READ_ONLY_NO_CHANGES (consumed)
Expected-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55

## Summary
Read-only functional audit of WLEDger at the deployed commit `ca2789f`. Static checks pass (build, vet, full tests, race on core packages). The system is generally healthy; the highest-value issues are an authorization gap around LED actions, a production cookie hardening TODO, dashboard completeness/readability, and absent CI. No source or production changes were made.

## Production / branch state (verified)
- Deployed commit `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`; goose 10; single controller with two strings (correct per user); drawer-relative conversion completed and verified (68/68). No third cabinet issue (Task 021).

## Checks run (isolated worktree at ca2789f5; no production touched)
- `go build ./...` → OK. `go vet -tags fts5 ./...` → clean.
- `go test -tags fts5 -count=1 ./...` → **39 packages ok, 0 FAIL**.
- `go test -race -tags fts5` on `internal/handler`, `internal/hardware`, `internal/hardware/mapper`, `internal/backup` → ok.
- `gofmt -l` → **44 files** flagged.
- CI (`.github/workflows/release.yml`) only builds/pushes Docker on tags; it runs **no** `go test`/`vet`/`gofmt`/static analysis.

## Prioritized findings

### HIGH
- **H1 — LED actions are in the read-auth group (authorization).** `internal/router/router.go` places `POST /hardware/{id}/locate`, `POST /parts/{id}/locate`, `POST /drawers/{id}/locate` and **`POST /hardware/off`** inside the `RequireReadAuth` group. Any authenticated user, including role `viewer` (`internal/auth/user.go`: `CanWrite` = admin|editor only), can therefore trigger LED actions and **turn all LEDs off** (`HandleGlobalOff`). Guests are excluded only because `require_auth_for_read` defaults to 1. Risk: least-privilege violation / disruptive hardware action by a read-only user. Fix: move locate/global-off to the write group (`RequireAuth` + `CanWrite`) or a dedicated "operate hardware" role.
- **H2 — Session cookie `Secure=false` in production.** `cmd/server/main.go:97`: `sessionManager.Cookie.Secure = false // TODO: Set to true in prod` (with `SameSite=Lax`). Behind an HTTPS proxy + HSTS the impact is reduced, but the cookie may be sent over plaintext. Risk: session disclosure on a network downgrade. Fix: make `Secure` env-configurable (default true; false only for local HTTP dev).

### MEDIUM
- **M1 — Dashboard hides controllers that have no grid-mapped bins.** `sql/queries/dashboard.sql` `GetDashboardGrid` uses `bins JOIN containers JOIN controllers ... WHERE b.grid_x IS NOT NULL AND b.grid_y IS NOT NULL`; `GetDashboardStats.total_controllers` counts **all** controllers. So a controller with no bins (or bins without grid positions) is counted in the stat but renders **no card** on the dashboard, even though `components.DashboardGrid` has an empty-state branch. Fix: `LEFT JOIN` controllers or fetch controllers separately and render the empty card.
- **M2 — No CI validation.** `release.yml` builds/pushes images only. Regressions are caught only by manual runs; the repo already has 44 non-gofmt files. Fix: add a CI job running `go test -tags fts5 ./...`, `go vet`, and `gofmt -l` on PRs/pushes.
- **M3 — Dashboard tile readability (matches the user's "tiny/overlapping text").** `web/components/dashboard_grid.templ` (legacy controller/string tiles) and `web/components/dashboard_wall.templ` render bin cells as fixed `w-12 h-12 ... text-xs` with the bin name inside and **no `truncate`/`overflow-hidden`**, and container headers use `flex items-center justify-between` with the name + `ID: N` and no truncation. Long names ("String 1 (LEDs 1-628)") or longer bin names can overflow/wrap awkwardly. Markup-verified; **visual confirmation NOT TESTED** (no authenticated session). Fix: add `truncate`/`min-w-0`/`line-clamp`, and consider slightly larger cells or ellipsis.
- **M4 — Not gofmt-clean (44 files).** Sample diff (`internal/qrcode/qr.go`) shows the cause is a **missing final newline**; `gofmt -l` fails repo-wide. Fix: run `gofmt -w` (mechanical) and enforce in CI (M2).

### LOW
- **L1 — Invalid Tailwind class.** `web/components/dashboard_wall.templ:99` uses `border-500/10` (not a valid utility); harmless but dead. Fix: remove or use `border-base-content/10`.
- **L2 — Hardcoded, non-i18n strings on the dashboard.** `dashboard_grid.templ` renders `"Containers"`, `"Empty"`, `"No containers configured."`, `"OK"/"Low"/"Critical"/"Empty"`, `"ID:"` literally, so they never localize despite 8 shipped locales. Fix: use `i18n.T`.
- **L3 — Startup performs automatic data writes.** `cmd/server/main.go` runs `MigrateLegacyLedIndices` and `BackfillDrawerAllocations` on every start. Both are gated (coordinate-space + system flags) and idempotent, so this is expected, not a defect — noted because it is an automatic write on startup.
- **L4 — Minor markup.** `<div class="badge ...">Empty</div>` uses a `div`; prefer `span`.

## Verified vs hypothesis
- Verified (source/tests): H1, H2, M1, M2, M4, L1, L2, plus the drawer auto-locate fix present in ca2789f (no `hx-trigger="load"`).
- Hypothesis needing visual confirmation: M3 (rendered tile overflow) — cannot be confirmed without an authenticated session; do not treat as a defect until reproduced in the UI.
- Out of scope / not tested: physical LED behaviour, responsive layout across viewports, Home Assistant integration end-to-end.

## Coverage gaps
- No template render test for the dashboard tiles (drawer/conversion/low-stock/part_detail have them).
- No test asserting controller-without-bins appears on the dashboard.
- No test/config asserting `Cookie.Secure`.
- Provider test coverage remains low (roadmap Priority 3).

## Proposed next 3 tasks (on ChatGPT review)
1. **Authorization & session hardening (High):** move locate/global-off to the write-auth group (or a hardware-operate role); add handler tests asserting viewer/guest are denied; make `Cookie.Secure` env-configurable. Small, self-contained.
2. **Dashboard completeness & tile readability (Medium):** render all controllers (LEFT JOIN) with the existing empty-state; add truncation/ellipsis and slightly larger tiles; add template render tests. Bounded UI change.
3. **CI + hygiene (Medium/Low):** add a CI workflow running `go test`/`vet`/`gofmt`; gofmt the 44 files; fix the invalid Tailwind class and i18n the hardcoded dashboard strings.

## Blockers / requests
- None blocking. Request: confirm priorities and, for M3, whether an authenticated UI walk-through can be provided (I must not bypass auth).

## References
- Commit: `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`
- Files: `internal/router/router.go`, `internal/auth/user.go`, `internal/middleware/middleware.go`, `cmd/server/main.go`, `sql/queries/dashboard.sql`, `internal/dashboard/service.go`, `web/components/dashboard_grid.templ`, `web/components/dashboard_wall.templ`, `.github/workflows/release.yml`, `docs/roadmap.md`.

## Boundaries respected
STRICTLY NO source or production changes; no migrations, DB writes, deploys/restarts, backup/restore, LED/WLED commands, or Home Assistant changes. Tests ran only in an isolated local worktree.
