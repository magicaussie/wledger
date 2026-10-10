# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 33
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 023B — CSRF Protection for Hardware LED Actions
Production-Authorization: NO_PRODUCTION_CHANGES (consumed)
Task-Branch: fix/hardware-auth-and-secure-cookie
Result: IMPLEMENTED ON TASK BRANCH (not merged, not deployed)

## Branch / commit
- Branch: `fix/hardware-auth-and-secure-cookie`
- Commit SHA: `7b5f63a11747310752aa2a186964d1970d36585f`
- Parent SHA: `b7a7055007422f40d6e8f869a1083ec3535d8430` (Task 023)
- `origin/main` unchanged at `ca2789f`; nothing deployed.

## Investigation
- The four endpoints are triggered by HTMX `hx-post` with **no** token: `web/pages/drawer.templ` (`/drawers/{id}/locate`), `web/components/locate_button.templ` (`/hardware/{id}/locate`), `web/components/part_locate_button.templ` (`/parts/{id}/locate`), `web/components/sidebar.templ` (`/hardware/off`, present on every page).
- Existing CSRF is session-bound: `middleware.CSRFToken` / `middleware.ValidateCSRF` (constant-time), supplied elsewhere as a hidden `csrf_token` form field (conversion form). There was no global HTMX CSRF configuration.
- Session cookie is `SameSite=Lax` (unchanged); CSRF is now enforced explicitly server-side, not relying on SameSite.

## Implementation
- **`internal/csrf` (new, dependency-free):** request-context carrier (`WithToken`/`TokenFromContext`). Introduced to avoid an import cycle (`layouts → middleware` would cycle via `middleware → uierror → web/pages → layouts`).
- **`internal/middleware/csrf.go`:** added `CSRFContext` (global; ensures an authenticated session has a token and exposes it via context; guests untouched) and `RequireCSRF` (validates `X-CSRF-Token` header, falling back to the `csrf_token` form field, via `ValidateCSRF`; 403 on missing/invalid).
- **`internal/router/router.go`:** `mw.CSRFContext` added globally after `Authenticate`; `mw.RequireCSRF` added to the hardware-action group after `RequireRole("editor","admin")`.
- **`web/layouts/base.templ`** (+ regenerated `base_templ.go`): renders `<meta name="csrf-token" content=…>` for authenticated users and loads `/static/js/csrf.js`.
- **`web/static/js/csrf.js` (new):** on `htmx:configRequest`, sets `X-CSRF-Token` from the meta tag for every HTMX request.

## Files changed
- `internal/csrf/csrf.go` (new), `internal/middleware/csrf.go`, `internal/router/router.go`
- `internal/router/router_auth_test.go`
- `web/layouts/base.templ`, `web/layouts/base_templ.go`, `web/static/js/csrf.js` (new)

## Tests
- Extended `internal/router` (real `router.New` + real middleware, fake WLED → no LED commands):
  - guest → 303 `/login`; viewer → 403 (unchanged).
  - editor/admin **with valid CSRF** → 200 on all four endpoints; exactly one WLED call each.
  - editor **missing CSRF** → 403; **invalid CSRF** → 403; **zero** WLED calls.
- `internal/config` `TestCookieSecure` unchanged (default Secure=true; opt-out documented).

## Checks run
- `gofmt -l` on changed files → clean. `go build ./...` → OK. `go vet -tags fts5 ./...` → clean.
- `go test -tags fts5 -count=1 ./...` → **40 packages ok, 0 FAIL**.
- `go test -race -tags fts5` on `router`, `handler`, `middleware`, `config`, `web/components`, `web/pages` → ok.

## Non-browser clients
- Home Assistant / MCP use `/api/v1` with bearer-token auth and are unaffected; the four browser routes now require a session-bound CSRF token. No client path was weakened.

## Risks
- A page loaded before this deploy lacks the `<meta name="csrf-token">`, so its HTMX Locate/Off would be rejected (403) until reloaded — expected and self-healing on refresh.
- `CSRFContext` writes a token to an authenticated session on first use (once per session); guests are untouched.
- Unchanged adjacent item (not in scope): the `lang` preference cookie still lacks `Secure`.

## Recommendation
- Ready for independent GitHub source review on `fix/hardware-auth-and-secure-cookie` (`7b5f63a`). Do not merge to `main` or deploy yet.

## Evidence / SHAs
- Task branch commit: `7b5f63a11747310752aa2a186964d1970d36585f` (parent `b7a7055007422f40d6e8f869a1083ec3535d8430`)
- Files: `internal/csrf/csrf.go`, `internal/middleware/csrf.go`, `internal/router/router.go`, `internal/router/router_auth_test.go`, `web/layouts/base.templ`, `web/layouts/base_templ.go`, `web/static/js/csrf.js`
