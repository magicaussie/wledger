# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 67
From: DeepSeek
To: ChatGPT
Status: AWAITING_REVIEW
Task: 039 — CSS Artifact Determinism and GitHub Actions Maintenance
Production-Authorization: NO_PRODUCTION_CHANGES (respected)
Expected-Main: 16e91f796c5e0c9b06884d55a3402e58e04365d2
Branch: chore/css-ci-maintenance
Commit: 2cf93a7560cf8f4b38afccd731a9c127fb8ad4de
Parent: 16e91f796c5e0c9b06884d55a3402e58e04365d2
Pull-Request: https://github.com/magicaussie/wledger/pull/4 (OPEN — not merged)
CI-Run: https://github.com/magicaussie/wledger/actions/runs/38054320709 (completed / success)
Result: SUCCESS — canonical minified CSS regenerated and committed; CI now verifies CSS determinism alongside Go/templ/sqlc; Actions modernized; PR #4 open with green CI. Not merged, not deployed.

## 1. Pre-flight (Task 1)

| Check | Expected | Actual | Result |
| --- | --- | --- | --- |
| `origin/main` | `16e91f796c5e0c9b06884d55a3402e58e04365d2` | `16e91f796c5e0c9b06884d55a3402e58e04365d2` | ✓ |
| Worktree | clean | clean (`git status --porcelain` empty) | ✓ |
| Branch created | `chore/css-ci-maintenance` | created from `16e91f7…` | ✓ |
| Toolchain | Go 1.25.5, templ v0.3.977, sqlc v1.29.0, Node 22 | Go 1.25.5, templ v0.3.977, sqlc v1.29.0, Node v22.23.2 / npm 10.9.8 | ✓ |

## 2. Canonical CSS command (Task 2)

`package.json` now carries a canonical production script that is **byte-identical** to the Dockerfile `css-builder` stage:

```
Dockerfile:  RUN npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --minify
package.json "build:css:prod": "npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --minify"
```

The pre-existing `build:css` (unminified, for local use) is retained unchanged. `npm ci` installs `@tailwindcss/cli@4.1.18` from the lockfile, so the CLI is available without a global install.

Regeneration: `npm ci && npm run build:css:prod`.

| Artifact | Bytes | Lines | Form |
| --- | --- | --- | --- |
| Old committed `output.css` (parent) | 164119 | 5622 | unminified, **stale** |
| New committed `output.css` | 174681 | 1 | **minified** (production form) |

- New artifact SHA256: `ae8a39055f92b4234a6f15836cdbb53002795ea8ac61c34c5b62c39f21f9e370`.
- `.gap-x-4` and `.gap-y-1` (used by `web/components/dashboard_wall.templ:42`) are now present — they were **absent** from the stale artifact.
- Determinism: two consecutive `build:css:prod` runs produced the **identical** SHA256; after staging the artifact, a re-run left `git diff --exit-code -- web/static/css/output.css` empty.
- Secret scan of the generated CSS: no secret-like strings.

## 3. CI changes (Task 3)

`.github/workflows/ci.yml`:

- Added `actions/setup-node@v4` with `node-version: "22"` and `cache: npm`.
- Added a **Verify committed CSS** step (after the existing generated-Go step):
  ```
  npm ci
  npm run build:css:prod
  git diff --exit-code -- web/static/css/output.css
  test -z "$(git status --porcelain --untracked-files=normal -- web/static/css)"
  ```
- Modernized `actions/checkout@v4 → @v5` and `actions/setup-go@v5 → @v6` (Node 24 actions).
- Go/sqlc/templ checks and fts5 tests left **unchanged**.

**Rationale / compatibility:** `checkout@v5` and `setup-go@v6` are the current Node 24 releases and ran cleanly on the hosted runner (no Node 20 deprecation annotation for them). `setup-node` was kept at `@v4` per the task instruction; it still targets Node 20 and therefore emits a deprecation annotation (see §6) — a `@v5` bump would remove it but was not requested.

## 4. Local validation (Task 4)

| Check | Command | Result |
| --- | --- | --- |
| YAML validity | PyYAML `safe_load` (accounting for the YAML 1.1 `on`→`True` trap) | valid; trigger = push(main) + pull_request |
| npm reproducibility | `npm ci` | 37 packages, success |
| CSS determinism | `build:css:prod` ×2 | identical SHA256 `ae8a3905…` |
| CSS utilities | grep `gap-x-4` / `gap-y-1` | present |
| templ determinism | `templ generate` | `updates=0` |
| sqlc determinism | `sqlc generate` | no drift |
| Build | `go build -tags fts5 ./...` | success |
| Vet | `go vet -tags fts5 ./...` | clean |
| Tests | `go test -tags fts5 -count=1 ./...` | **40 packages `ok`**, 0 failures |

Diff scope `16e91f7..2cf93a7` — exactly the three scoped files:

| Status | Path |
| --- | --- |
| M | `.github/workflows/ci.yml` |
| M | `package.json` |
| M | `web/static/css/output.css` |

`3 files changed, 23 insertions(+), 5624 deletions(-)`. No application logic, Dockerfile, migrations, SQL, auth, WLED or production config changes. `package-lock.json` required no change.

## 5. Pull request and CI (Task 5)

- Pushed `chore/css-ci-maintenance` → `origin` (new branch; no force push).
- PR **#4** opened against `main`: https://github.com/magicaussie/wledger/pull/4 — state **OPEN**, `MERGEABLE` / `mergeStateStatus CLEAN`, head `2cf93a7…`.
- CI run **`38054320709`** (event `pull_request`, branch `chore/css-ci-maintenance`, sha `2cf93a7…`) → **completed / success** (3m4s). All steps green: Checkout → Set up Go → Set up Node → Add Go bin to PATH → Install generators → **Verify committed generated Go** → **Verify committed CSS** → Build → Vet → Test.
  - URL: https://github.com/magicaussie/wledger/actions/runs/38054320709

## 6. Warnings / observations

1. **`actions/setup-node@v4` Node 20 deprecation annotation** (non-blocking): the runner forces it onto Node 24. Kept at `@v4` per task instruction; a future `@v5` bump would clear it.
2. **`ubuntu-latest` → Ubuntu 26 migration notice** (Oct 2026) — informational only.
3. **Dockerfile unchanged.** The tracked CSS now matches the production minified form because the canonical script and the Docker CSS stage are the same command over the same lockfile/dependencies.
4. **No production impact.** This PR only regenerates a tracked build artifact and hardens CI; the running production container is unaffected.

## Evidence / SHAs

- Parent / expected `main`: `16e91f796c5e0c9b06884d55a3402e58e04365d2`
- Task commit: `2cf93a7560cf8f4b38afccd731a9c127fb8ad4de`
- PR: https://github.com/magicaussie/wledger/pull/4 — **OPEN (not merged)**
- CI (PR): https://github.com/magicaussie/wledger/actions/runs/38054320709 — **success**
- New CSS SHA256: `ae8a39055f92b4234a6f15836cdbb53002795ea8ac61c34c5b62c39f21f9e370`

## Boundaries respected

No merge, no production deployment or restart, no live DB change, no Wall creation, no physical LED/WLED calls, no Home Assistant change, no secrets printed, no production checkout edits, no force push. `main` remains at `16e91f7…`; all LED mappings and running production remain untouched. **STOP — awaiting review.**
