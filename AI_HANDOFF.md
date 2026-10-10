# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 31
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 023 — Hardware Action Authorization and Secure Session Cookies
Production-Authorization: NO_PRODUCTION_CHANGES (consumed)
Base-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Result: IMPLEMENTED ON TASK BRANCH (not merged, not deployed)

## Branch / commit
- Branch: `fix/hardware-auth-and-secure-cookie`
- Commit SHA: `b7a7055007422f40d6e8f869a1083ec3535d8430`
- Parent SHA: `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`
- `origin/main` unchanged at `ca2789f`; nothing deployed; no production changes.

## Changes
1. **H1 — LED actions now require a write-capable role.** In `internal/router/router.go`, `POST /hardware/{id}/locate`, `POST /parts/{id}/locate`, `POST /drawers/{id}/locate` and `POST /hardware/off` were moved out of the `RequireReadAuth` group into a new group inside the authenticated write group using `mw.RequireRole("editor", "admin")` (so: `RequireAuth` + `RequirePasswordChange` + `RequireRole`). Paths, methods, the explicit manual Locate, and handler behaviour are unchanged. Read-only GET routes are untouched.
2. **H2 — Secure session cookies by default.** `cmd/server/main.go` now sets `sessionManager.Cookie.Secure = config.CookieSecure()` (was hardcoded `false`). New `internal/config/security.go` adds `CookieSecure()`: default **true**; disabled only when `WLEDGER_INSECURE_COOKIES` is truthy (`1/true/yes/on`, case-insensitive) for deliberate local HTTP development. This is compatible with the production TLS-terminating reverse proxy (browser↔proxy is HTTPS).
3. **Docs:** new `docs/environment.md` documents `WLEDGER_PUBLIC_URL`, `WLEDGER_API_TOKEN`, `WLEDGER_INSECURE_COOKIES` (with the local-dev warning) and the MCP variables. No secrets.

## Files changed
- `internal/router/router.go` (route group move)
- `cmd/server/main.go` (cookie Secure via config)
- `internal/config/security.go` (new), `internal/config/security_test.go` (new)
- `internal/router/router_auth_test.go` (new)
- `docs/environment.md` (new)

## Tests added
- `internal/config` `TestCookieSecure`: default true; truthy values disable; other values keep Secure.
- `internal/router` (router-level, real `router.New` + real middleware chain, fake WLED so no LED commands):
  - `TestHardwareActionsRequireAuthentication` — guest → 303 `/login` on all four endpoints; 0 WLED calls.
  - `TestHardwareActionsDeniedForGuestWhenReadIsPublic` — with `require_auth_for_read=false`, guest still → 303; 0 WLED calls.
  - `TestHardwareActionsDeniedForViewer` — viewer → 403 on all four; 0 WLED calls.
  - `TestHardwareActionsAllowedForEditorAndAdmin` — editor and admin → 200 on all four; exactly one call each to `LocateBin`/`LocatePart`/`LocateDrawer`/`GlobalOff`.
- CSRF: these routes have no CSRF middleware in their group (unchanged), so the authorization tests exercise the real chain without CSRF; no CSRF bypass was introduced.

## Checks run
- `gofmt -l` on changed files → clean.
- `go build ./...` → OK. `go vet -tags fts5 ./...` → clean.
- `go test -tags fts5 -count=1 ./...` → **40 packages ok, 0 FAIL**.
- `go test -race -tags fts5` on `router`, `handler`, `middleware`, `config` → ok.

## Adjacent findings (documented, NOT changed — scope respected)
- The `lang` preference cookie set in `internal/middleware/middleware.go` (`I18n`) has `HttpOnly` but no `Secure`. Low risk (non-session preference); candidate for a small follow-up.
- The four moved endpoints are **not CSRF-protected** (pre-existing). A logged-in user could be induced to trigger locate/global-off cross-site. Recommend a follow-up to add CSRF validation to these POSTs (medium).
- Other hardware-operating routes (`/hardware` create/import/delete, `/hardware/{id}/grid`, `/hardware/conversion`) are already admin-only; no other read-group hardware actions were found.

## Risks
- Behaviour change: `viewer` (and guests) can no longer trigger locate/global-off — intended. Editors/admins unaffected.
- `Cookie.Secure=true` by default will break plain-HTTP local use unless `WLEDGER_INSECURE_COOKIES=1`; documented. Production (HTTPS proxy) unaffected.
- No production deploy/restart, DB/migration, backup/restore, LED/WLED calls, or HA changes were performed.

## Recommendation
- Ready for independent GitHub source review on `fix/hardware-auth-and-secure-cookie` (`b7a7055`). Do not merge to `main` yet. Suggested follow-ups (separate tasks): add CSRF to the hardware-action POSTs; add `Secure` to the `lang` cookie.

## Evidence / SHAs
- Task branch commit: `b7a7055007422f40d6e8f869a1083ec3535d8430` (parent `ca2789f54382bc2aa98b2d4535b09f9df1c21d55`)
- Files: `internal/router/router.go`, `cmd/server/main.go`, `internal/config/security.go`, `internal/config/security_test.go`, `internal/router/router_auth_test.go`, `docs/environment.md`
