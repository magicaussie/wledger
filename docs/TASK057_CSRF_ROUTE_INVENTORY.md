# Task 057 — Browser mutation-route inventory (baseline)

- **Baseline:** `478bcd5235760e32a604b25a515def5d24b76a08`
- **Scope:** routes registered in `internal/router/router.go` and browser-session token delivery before remediation. Chi path parameters are shown as `{name}`. `/api/v1` and network MCP transports are inventoried separately because they use independent machine authentication.

## Browser-session mutation routes

`CSRF enforced` means a route had either `RequireCSRF` middleware or explicit handler validation at baseline. Merely sending a token from the UI did not count as enforcement.

| Method | Path | Authentication / role | Baseline CSRF enforcement | Baseline UI token delivery |
|---|---|---|---|---|
| POST | `/setup` | Public, first-run only | No | No token in setup form |
| POST | `/login` | Public | No | No token in login form |
| POST | `/logout` | Session route; handler destroys session | No | No token in sidebar/force-reset logout forms |
| POST | `/force-reset` | Authenticated session; password-reset-required user | No | No token in reset form |
| POST | `/settings/password` | Authenticated session | No | No token in password form |
| POST | `/hardware/{id}/locate` | Authenticated editor/admin | Yes, hardware-action group | HTMX global header from `web/static/js/csrf.js` |
| POST | `/parts/{id}/locate` | Authenticated editor/admin | Yes, hardware-action group | HTMX global header |
| POST | `/parts/{id}/locate-bin` | Authenticated editor/admin | Yes, hardware-action group | Hidden form token; HTMX also adds header when used |
| POST | `/storage/drawers/{id}/locate` | Authenticated editor/admin | Yes, hardware-action group | Hidden form token |
| POST | `/storage/drawers/{id}/stop` | Authenticated editor/admin | Yes, hardware-action group | Hidden form token |
| POST | `/drawers/{id}/locate` | Authenticated editor/admin | Yes, hardware-action group | HTMX global header |
| POST | `/hardware/off` | Authenticated editor/admin | Yes, hardware-action group | HTMX global header |
| POST | `/storage/cabinets/{id}/controller` | Authenticated admin | Yes, selected cabinet group | Dynamic form in physical-layout JS includes hidden token |
| POST | `/storage/cabinets/{id}/wiring` | Authenticated admin | Yes, selected cabinet group | Dynamic form includes hidden token |
| POST | `/hardware/{id}/locate-mapping` | Authenticated admin | Yes, selected cabinet group | Hidden form token |
| POST | `/storage/cabinets` | Authenticated editor/admin | Yes, layout-write group | Hidden form token |
| POST | `/storage/cabinets/{id}/edit` | Authenticated editor/admin | Yes, layout-write group | Hidden form token |
| POST | `/storage/cabinets/{id}/drawers/{drawerID}/led` | Authenticated editor/admin | Yes, layout-write group | Dynamic form includes hidden token |
| POST | `/storage/cabinets/{id}/archive` | Authenticated editor/admin | Yes, layout-write group | Dynamic form includes hidden token |
| POST | `/storage/cabinets/{id}/delete` | Authenticated editor/admin | Yes, layout-write group | Dynamic form includes hidden token |
| POST | `/storage/cabinets/{id}/drawers/{drawerID}/archive` | Authenticated editor/admin | Yes, layout-write group | Dynamic form includes hidden token |
| POST | `/storage/cabinets/{id}/drawers/{drawerID}/delete` | Authenticated editor/admin | Yes, layout-write group | Dynamic form includes hidden token |
| POST | `/storage/cabinets/{id}/bins/{binID}/detach` | Authenticated editor/admin | Yes, layout-write group | Dynamic form includes hidden token |
| POST | `/parts` | Authenticated editor/admin | **No** | Multipart part form had no hidden token |
| POST | `/parts/import` | Authenticated editor/admin | **No** | HTMX global header was sent |
| POST, DELETE | `/parts/bulk` | Authenticated editor/admin | **No** | Bulk form/action depended on UI; no route guard |
| POST | `/parts/{id}/update` | Authenticated editor/admin | **No** | Multipart part form had no hidden token |
| POST | `/parts/{id}/delete` | Authenticated editor/admin | **No** | Confirmation modal form had no hidden token |
| POST | `/parts/{id}/favorite` | Authenticated editor/admin | **No** | HTMX global header was sent |
| DELETE | `/parts/links/{id}` | Authenticated editor/admin | **No** | HTMX global header was sent |
| DELETE | `/parts/docs/{id}` | Authenticated editor/admin | **No** | HTMX global header was sent |
| POST | `/parts/{id}/assign` | Authenticated editor/admin | **No** | Part assignment form had no hidden token |
| POST | `/parts/{id}/stock/{assignment_id}/adjust` | Authenticated editor/admin | **No** | HTMX global header was sent |
| POST | `/parts/{id}/stock/{assignment_id}/move` | Authenticated editor/admin | **No** | Move-stock form had no hidden token |
| POST | `/parts/{id}/stock/{assignment_id}/delete` | Authenticated editor/admin | **No** | Standard form had no hidden token |
| POST | `/suppliers/import` | Authenticated editor/admin | **No** | Supplier import form had no hidden token |
| POST | `/suppliers/url/parse` | Authenticated editor/admin | **No** | HTMX global header was sent; this is a parse action, not a persistent write |
| POST | `/inspiration` | Authenticated editor/admin | **No** | HTMX global header was sent |
| PUT | `/inspiration/{id}` | Authenticated editor/admin | **No** | HTMX global header was sent |
| DELETE | `/inspiration/{id}` | Authenticated editor/admin | **No** | HTMX global header was sent |
| POST | `/hardware` | Authenticated admin | **No** | Controller-create form had no hidden token |
| POST | `/hardware/import` | Authenticated admin | **No** | Multipart hardware-import form had no hidden token |
| POST | `/hardware/{id}/delete` | Authenticated admin | **No** | Controller-delete form had no hidden token |
| POST | `/hardware/{id}/grid` | Authenticated admin | **No** | Grid form had no token; JS calls `form.submit()` |
| POST | `/hardware/{id}/capacity` | Authenticated admin | Yes, capacity sub-group | Dynamic form includes hidden token |
| POST | `/hardware/conversion` | Authenticated admin | Yes, handler-level `ValidateCSRF` | Hidden form token |
| POST | `/settings` | Authenticated admin | **No** | Settings form had no hidden token |
| POST | `/settings/backup/restore` | Authenticated admin | **No** | HTMX global header was sent for multipart request |
| POST | `/settings/users` | Authenticated admin | **No** | User-create form had no hidden token |
| POST | `/settings/users/{id}/delete` | Authenticated admin | **No** | User-delete form had no hidden token |
| POST | `/settings/users/{id}/reset` | Authenticated admin | **No** | Password-reset form had no hidden token |
| POST | `/walls` | Authenticated admin | **No** | Dashboard wall-create form had no hidden token |
| POST | `/walls/{id}` | Authenticated admin | **No** | Wall-edit form had no hidden token |
| POST | `/walls/{id}/delete` | Authenticated admin | **No** | Dynamic form in `web/static/js/wall_edit.js` had no token |
| POST | `/suppliers/credentials` | Authenticated admin | **No** | Credential form had no hidden token |
| POST | `/suppliers/price-snapshot` | Authenticated admin | **No** | Price-snapshot form had no hidden token |

There are no registered browser `PATCH` routes at this baseline. `POST /suppliers/url/parse` is non-persistent but is still protected as a mutation verb by the central policy.

## Machine-authenticated routes

| Method | Path | Authentication | Browser CSRF handling |
|---|---|---|---|
| GET | `/api/v1/health` | `Authorization: Bearer` or accepted `Token` scheme; constant-time comparison | Not applicable |
| POST | `/api/v1/global-off` | Same bearer middleware | Exempt from session CSRF only because bearer authentication is required |
| POST | `/api/v1/parts/{id}/locate` | Same bearer middleware | Exempt from session CSRF only because bearer authentication is required |
| POST | `/api/v1/bins/{id}/locate` | Same bearer middleware | Exempt from session CSRF only because bearer authentication is required |
| GET | `/api/v1/parts` | Same bearer middleware | Not applicable |
| GET | `/api/v1/parts/{id}` | Same bearer middleware | Not applicable |
| GET | `/api/v1/hardware` | Same bearer middleware | Not applicable |
| POST/GET | MCP SSE/streamable HTTP transport endpoints | Separate `WLEDGER_API_TOKEN` bearer auth plus MCP Origin policy; stdio is local client-launched transport | Must remain independent of browser session CSRF |

The central browser policy must not skip authentication for these paths: `/api/v1` remains protected by its own token middleware. MCP is a separate server in `cmd/mcp-server`, not a WLEDger browser-router subtree.

## State-changing GET assessment

| Method / path | Observed side effect | Assessment |
|---|---|---|
| GET `/hardware/{id}/status` | Reads/pings the saved controller and updates cached online status in SQLite | Read-refresh/cache side effect only; keep GET behavior for this remediation. It does not send WLED state commands. |
| GET `/suppliers/oauth/start` | Generates an OAuth state and redirects to the provider | User-initiated redirect; state must be bound to the authenticated session and checked on callback. |
| GET `/suppliers/oauth/callback` | Exchanges authorization code and persists credentials | **Baseline state validation was incomplete:** the handler passed through a formatted `provider:random` value without storing/comparing it. Add session-bound, single-use state validation; this callback remains GET because OAuth providers redirect with GET. |
| GET `/inspiration/{id}/generate` | Constructs/returns a prompt | Read-only; no persistent mutation observed. |
| GET `/settings/backup/download` | Builds a temporary archive and streams it; export service may record audit history | Download/read path; no inventory/configuration mutation. Preserve GET. |
| Other registered GETs | Read/list/detail/preview/output discovery/QR/image-proxy operations | No application state mutation found in the inspected handlers; any network reads remain read-only. |

## Token propagation conclusion

At baseline, `web/static/js/csrf.js` added the session token to **HTMX requests only**, using `X-CSRF-Token` from the authenticated page's `<meta name="csrf-token">`. Standard form submissions and several dynamic `form.submit()` paths lacked hidden fields. This gap is addressed by the common middleware and the template/form propagation changes tracked by Task 057; the bearer API and MCP contracts remain separate.

## Task 057 remediation result

**Status:** implemented and locally verified. Full verification commands/results follow below.

- `middleware.RequireCSRF` is installed once at the root browser router and checks `POST`, `PUT`, `PATCH`, and `DELETE`. All browser mutation routes in the table above now inherit it; duplicated route-group guards were removed. Safe methods continue through unchanged.
- Browser-session authenticated mutations require a session-bound token. Unauthenticated requests to other protected routes continue to authentication/role middleware, preserving existing login redirects and authorization. `/login`, `/setup`, and `/logout` have explicit token enforcement. Anonymous login/setup GETs create a pre-authentication session token rendered only as a hidden form field (not in the shared authenticated-page meta tag); successful login/setup rotates it, and the next authenticated GET creates the new authenticated token. Login failure renders retain the current pre-auth token.
- Multipart forms are parsed for the hidden token only when no `X-CSRF-Token` header was supplied. Parser-created temporary files are removed on both accept and reject paths. Failed CSRF checks precede application handlers, and regression tests verify unchanged database state, uploads, mock WLED calls, and no leftover multipart temporary files.
- `web/components/csrf.templ` defines the `CSRFInput` component, which supplies hidden tokens to regular, multipart, and modal forms. `web/static/js/csrf.js` continues supplying `X-CSRF-Token` on HTMX requests; dynamic wall deletion reads the authenticated page meta token, and the legacy grid form now contains a hidden token for its programmatic `.submit()` call. CSRF tokens are not placed in query strings. Request logging redacts sensitive query keys, including OAuth `state` and `code`.
- The OAuth callback remains a GET redirect endpoint but now requires the exact single-use state saved in the initiating authenticated session; comparison is constant-time and successful state is consumed before code exchange. Missing, mismatched, and replayed values are rejected before supplier credential writes.
- `/api/v1` is exempted from browser CSRF only by path; the mounted API still requires its own bearer/token authentication. MCP routes remain on their separate bearer plus Origin middleware; no MCP or API authorization bypass was added.

### Previously unprotected browser mutations now guarded

The prior uncovered route groups were parts and stock operations, bulk deletion, supplier import/credential/price actions, inspiration writes, legacy controller create/import/delete/grid, settings/password/user administration, backup restore, and wall management. The baseline table gives the complete method/path and role inventory; every such browser mutation now receives the root CSRF policy. Existing hardware-action, cabinet-layout, controller/wiring, capacity, and coordinate-conversion checks remain protected (the conversion handler retains its direct token check as defense in depth).

### Regression and browser evidence

- `internal/router/router_csrf_test.go` discovers every registered browser mutation method/path from the router, verifies missing and invalid tokens reject before DB/file/WLED effects, and submits valid tokens through each handler path. Additional tests cover viewer/unauthenticated authorization, setup/login forms, multipart temporary-file cleanup, and bearer API operation without browser CSRF.
- `internal/handler/suppliers_csrf_test.go` verifies OAuth state is session-bound and single-use; the router test verifies an unbound callback never reaches credential exchange.
- Existing API bearer tests and MCP token/Origin tests remain unchanged and pass.
- `web/tests/csrf.browser.test.js` uses a fresh disposable SQLite DB and a real browser to exercise setup/login/logout, tokenless request rejection, multipart part create/image/edit, HTMX stock adjustment, settings, user create/delete, backup download, tokenless multipart restore rejection, and valid restore. The layout browser suite separately verifies cabinet/controller/wiring/allocation forms plus mocked Locate/Stop continue to work.
- The new CSRF browser scenario also observes two existing `html5QrcodeScanner` duplicate-declaration page errors while navigating inventory screens. Its CSRF workflows still pass; this unrelated scanner issue was not changed in Task 057. The two expected HTTP 403 console messages are from deliberate tokenless negative requests.

### Race-test investigation

The earlier intermittent `ErrHardwareBusy` in drawer-space tests was caused by test fixture teardown clearing only the primary `PhysicalLocateService` reservation. In `TestPhysicalLocateTwoConnectionsConcurrentLocateHasOneDurableOwner`, the second service can own the successful active session, leaving process-global mock endpoint/controller gates behind for subsequent tests. Fixture cleanup now stops the primary timer, walks the fixture's durable session/target rows to release every mock endpoint/controller reservation (including a winning session owned by the second service), and asserts no process-local owner remains. `go test -race -tags fts5 -count=10 ./internal/wled` passed after this correction.

## Verification and operational limits

| Check | Result |
|---|---|
| `CGO_ENABLED=1 go test -tags fts5 -count=1 ./...` | **PASS** |
| `CGO_ENABLED=1 go test -race -tags fts5 -count=1 ./...` | **PASS** |
| `CGO_ENABLED=1 go test -race -tags fts5 -count=10 ./internal/wled` | **PASS** — repeated after fixing fixture teardown |
| `CGO_ENABLED=1 go vet -tags fts5 ./...` | **PASS** |
| `CGO_ENABLED=1 go build -tags fts5 ./cmd/server ./cmd/mcp-server` | **PASS** |
| `npm test` | **PASS** — 20 behavior tests |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:csrf-browser` | **PASS** — fresh temporary DB; setup/login/logout; native multipart part create/edit/image; HTMX stock adjustment; settings/user create/delete; backup download and valid restore; missing-token mutations/restores rejected before effects |
| `GO_BIN=/home/spetchal/gotoolchain/go/bin/go npm run test:layout-browser` | **PASS** — existing cabinet/controller/wiring/allocation and five mock-controller Locate/Stop journeys; no page/runtime console errors |
| Templ generation and JS syntax checks | **PASS** — `templ generate`, `node --check` for `csrf.js`, `wall_edit.js`, and the CSRF browser test |
| `git diff --check` | **PASS** |

The CSRF browser test records two expected console messages for its deliberately rejected 403 requests and two pre-existing `html5QrcodeScanner` duplicate-declaration page errors. No unexpected page/console errors occurred. This scanner issue is unrelated and was not changed. All application/browser DBs and uploads were temporary; the existing `.task055b-staging/` tree was not opened, reset, or used. No request was sent to port 8090 and no physical WLED controller was contacted.

**Remaining limits:** no real OAuth provider was contacted; OAuth state behavior is covered by session helper and router tests. The bearer API and MCP transports are integration/unit-tested with their existing token/Origin checks, not run as deployed services. The independent GitHub source review remains required after publication.
