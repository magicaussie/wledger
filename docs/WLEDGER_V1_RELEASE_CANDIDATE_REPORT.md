# WLEDger v1.0 — Final Security Triage and Release Candidate

**Date:** 2026-10-14
**Authorized branch:** `development/chatgpt-m6-m7`
**Tested baseline:** `3bf31146017455019187610392d51be1a55b148d`
**Release profile:** Inventory-only; physical WLED output disabled by default

## Decision

**READY FOR OWNER ACCEPTANCE**

The source-level Go scan has no vulnerable call traces or vulnerable imported packages. The one remaining Go database record applies to the unused `golang.org/x/crypto/openpgp` package, not to WLEDger's bcrypt use. The remaining npm audit findings are confined to development/build tooling and are not packaged in the runtime image. All requested functional, regression, build, race, and loopback-only recovery gates passed.

This is an owner-acceptance decision for the inventory-only candidate, not authorization to merge, tag, deploy, access production/staging, or contact physical WLED hardware.

## Scope, baseline, and preservation

- At the start, `HEAD`, `origin/development/chatgpt-m6-m7`, and the requested published baseline all resolved to `3bf31146017455019187610392d51be1a55b148d` on the authorized branch.
- Preserved and did not stage or modify the four pre-existing user-owned paths: `AGENTS.md`, `docs/OMO_HANDOVER.md`, `.task055b-staging/`, and `docs/TASK056_FUNCTIONAL_AUDIT.md`.
- Retained the pre-existing security dependency updates and the narrow browser-harness corrections. No reset, stash, clean, or blanket staging was used.
- Browser acceptance used fresh temporary directories, disposable SQLite databases, and `127.0.0.1` mock-controller ports. No traffic was sent to ports 8090/8091; no production/staging database or service, real WLED controller, or physical hardware was contacted.

## Go toolchain and dependency decisions

Go `1.26.9` is available and appropriate for this project. The project compiles, tests, races, vets, and builds under it. `golang.org/x/net v0.60.0` requires Go `1.26.0`; holding the project at Go 1.25 would prevent selecting that fixed release. The Go directive, Docker builder, and CI toolchain are aligned accordingly.

| Item | Final version | Reason |
|---|---:|---|
| Go directive | `1.26.0` | Minimum required by selected `x/net` release. |
| Go toolchain / Docker builder / CI | `1.26.9` | Patched toolchain; builder tag existence checked with `docker manifest inspect`. |
| `github.com/go-chi/chi/v5` | `v5.3.0` | Upstream fixes for Chi advisories reported on the baseline. |
| `golang.org/x/crypto` | `v0.57.0` | Required by the selected `x/net` graph; current application uses bcrypt/blowfish. |
| `golang.org/x/image` | `v0.45.0` | Fixes the reachable image-decoder advisory. |
| `golang.org/x/net` | `v0.60.0` | Fixes the HTML parser/module findings and permits the Go 1.26 fix level. |
| `golang.org/x/sync` | `v0.23.0` | Required graph update selected with `x/net`. |
| `golang.org/x/sys` | `v0.48.0` | Required graph update selected with `x/net`. |
| `golang.org/x/text` | `v0.42.0` | Required graph update selected with `x/net`. |

The server and MCP build image uses `golang:1.26.9-alpine`; CI uses Go 1.26.9. Module graph review found `x/net/html` and `html/atom`, not `x/net/http2`. No unrelated Go project modules were upgraded.

## Security inventory and dispositions

### Resolved release-relevant findings

- **`GO-2026-6222` — `golang.org/x/image` VP8L decoder.** Baseline `x/image` was an old pseudo-version; malformed uploaded or supplier-fetched image data could reach production image processing (`internal/images.ProcessBytes`, upload/import call paths). Updated to `v0.45.0`, the fixed release. Focused image and supplier tests and the full Go suites pass.
- **`GO-2026-5028`, `GO-2026-5029`, `GO-2026-5030` — `golang.org/x/net/html` parser.** Supplier response HTML reaches Jaycar parsing (`internal/suppliers/providers/jaycar.ParseProductPage`). Updated from `v0.52.0` through `v0.55.0` to `v0.60.0`; those parser findings are absent from the final scan. Supplier tests pass.
- **Additional `x/net` module findings, including `GO-2026-5942`, `GO-2026-6603`, `GO-2026-6610`, `GO-2026-6611`, `GO-2026-6612`, and `GO-2026-6617`.** Updated to `x/net v0.60.0`; none remain in the final scan. The standard-library fix level is Go `1.26.9`; no standard-library advisories remain in the final scan.
- **Chi `GO-2026-4316`, `GO-2026-5774`, `GO-2026-5775`, and `GO-2026-5777`.** Updated Chi from `v5.2.3` to `v5.3.0`, which covers the reported fix levels. Removed `middleware.RealIP` from `internal/router/router.go`: it was not needed for authorization and only rewrote `r.RemoteAddr` used in request/warning logs. Arbitrary `Forwarded`, `X-Forwarded-For`, and `X-Real-IP` values no longer influence that logged peer address. The router regression test verifies this.
- **Supplier OAuth scheme trust.** Separately, the OAuth-start handler previously trusted request-supplied `X-Forwarded-Proto` when `WLEDGER_PUBLIC_URL` was empty. It now derives a fallback scheme from the actual TLS connection only; a TLS-terminating proxy must configure `WLEDGER_PUBLIC_URL` to its external HTTPS URL. A focused test verifies forged forwarding headers are ignored. The normal Compose configuration already supplies `WLEDGER_PUBLIC_URL`.

### Remaining Go database record — accepted residual, not a release blocker

| Advisory | Affected package / installed module | Fixed version | Source call path and WLEDger entry point | Untrusted input / vulnerable configuration | Target and disposition |
|---|---|---|---|---|---|
| `GO-2026-5932` | `golang.org/x/crypto/openpgp` and subpackages (`openpgp/packet`, `armor`, `clearsign`, `errors`, `elgamal`, `s2k`); module `golang.org/x/crypto v0.57.0` | None; the advisory describes the package as unmaintained/unsafe by design. | **No vulnerable symbol trace.** The structured finding contains only the module/version. `govulncheck` reports zero vulnerable imported packages and says the module advisory is not called. WLEDger's module entry points are bcrypt/blowfish; source uses password hashing/verification (`internal/auth` and password settings), not OpenPGP. `openpgp` is absent from the `./...` test and server/MCP dependency package lists. | User passwords reach bcrypt, not OpenPGP. No OpenPGP feature, parser, configuration, or data path is present. | Not exploitable through WLEDger's deployed server or MCP binary, nor tests/development code. Accepted as a module-metadata residual; no safe corrective action is warranted without importing OpenPGP functionality. |

Final `govulncheck v1.9.0` source scan used Go `1.26.9` and the Go vulnerability DB last modified `2026-10-08T22:31:09Z`. It reported **0 source-reachable vulnerabilities, 0 vulnerable imported packages, and 1 module-only record (`GO-2026-5932`)**. Finding classification is based on symbols, entry points, input reachability, and configuration—not the raw record count.

### npm audit residuals — development/build-only

`npm audit --omit=dev` reports **0 vulnerabilities**. Full `npm audit` reports five high-severity vulnerable package nodes in root dev dependencies, with remediation available:

| Advisory | Installed affected package(s) | Vulnerable operation / entry point | Runtime/configuration status and disposition |
|---|---|---|---|
| `GHSA-vfj7-8cjw-p6xm` | `braces@3.0.3` (via `micromatch@4.0.8` → `@parcel/watcher@2.5.1` → Tailwind CLI) | Deeply nested pattern processing; no WLEDger HTTP/API entry point. | Build/test tooling only. `npm ci` and CSS generation run in the Docker CSS-builder stage; these packages are not copied into the runtime image. No untrusted runtime patterns are supplied. Accepted non-blocking residual; no broad npm lockfile upgrade was made. |
| `GHSA-3v7f-55p6-f55p`, `GHSA-c2c7-rcm5-vvqj` | `picomatch@2.3.1` (same Tailwind watcher chain) | Glob matching / extglob ReDoS; no WLEDger HTTP/API entry point. | Same build-only boundary; no attacker-controlled runtime glob patterns. `npm audit` reports a fix available. Accepted non-blocking residual. |
| `GHSA-68fv-2mgg-jv7q` | `source-map-js@1.2.1` (via `@tailwindcss/node`) | Indexed source-map section processing; no deployed application source-map parser entry point. | Tailwind build tooling only and not included in the runtime image. `npm audit` reports a fix available. Accepted non-blocking residual. |

These packages are under `devDependencies`; the runtime image receives generated CSS, not the root npm dependency tree. This is a build-supply-chain residual, not a runtime vulnerability in the inventory server or MCP server.

## Verification results

All commands below were run against this final working-tree state unless the row describes the isolated disposable browser harness. Go commands used Go `1.26.9` and `CGO_ENABLED=1` where needed.

| Gate | Result |
|---|---|
| Structured `govulncheck -json -tags fts5 ./...` and final text scan | **PASS** — zero source findings / vulnerable imported packages; one accepted module-only `GO-2026-5932` residual. |
| `go test -count=1 -tags fts5 ./...` | **PASS** |
| `go test -race -count=1 -tags fts5 ./...` | **PASS** |
| `go vet -tags fts5 ./...` | **PASS** |
| Server and MCP builds (`./cmd/server`, `./cmd/mcp-server`) | **PASS** — binaries built to `/tmp/wledger-v1-server` and `/tmp/wledger-v1-mcp`. |
| Focused image/supplier tests (`./internal/images ./internal/suppliers/...`) | **PASS** |
| Output-disabled API/MCP guard tests (`./internal/api ./internal/wled ./cmd/mcp-server`) | **PASS** |
| `npm test` | **PASS** — all 20 grid-painter tests. |
| `npm run test:csrf-browser` | **PASS** — 12 mutations, no unexpected page/runtime errors. |
| `npm run test:inventory-lifecycle` | **PASS** — new disposable installation, output explicitly disabled, image/inventory lifecycle, restart, separate-app restore, and startup journal recovery; zero WLED writes during restore/recovery. |
| `npm run test:layout-browser` | **PASS** — editor, physical-first, scanner behavior; QR identities decoded with OpenCV and Code 128 with ZXing. Physical output was false and the physical-first test recorded `wledRequests: []`. |
| `npm run test:task065-restore` | **PASS at process/test-assertion level** — exit 0; harness's overall JSON label is `PARTIAL`, while Locate/Stop, SIGKILL recovery, and separate-app restore each reported **PASS** against disposable loopback mocks. No `GlobalOff` was used. |
| `npm run test:fresh-install` and `node web/tests/ui_only_install.browser.test.js` | **PASS at process/test-assertion level** — exit 0; harness JSON label `PARTIAL` denotes its broader coverage label. Negative validation cases were rejected as expected; page errors/findings were empty for the fresh-install harness. |
| `go mod verify`, `go mod tidy -diff`, `git diff --check` | **PASS** |
| npm audits | **PASS with documented dev-only residuals** — production audit has zero findings; full audit has the entries above. |

No end-to-end Docker image build was run; the `golang:1.26.9-alpine` manifest was verified and the server/MCP binaries were built directly. No production/staging access or physical WLED contact occurred.

## Scoped publication contents

The intended task files are `.github/workflows/ci.yml`, `Dockerfile`, `docs/environment.md`, this report, `go.mod`, `go.sum`, `internal/handler/suppliers.go`, `internal/handler/suppliers_csrf_test.go`, `internal/router/router.go`, `internal/router/router_test.go`, `web/tests/task064_fresh_install.browser.test.js`, and `web/tests/ui_only_install.browser.test.js`. The four protected user-owned paths above are excluded from staging and publication.

The candidate is ready for publication to `development/chatgpt-m6-m7` after the lead review and branch/remote/staging checks. No merge, tag, deployment, production/staging access, or physical WLED operation is authorized by this report.
