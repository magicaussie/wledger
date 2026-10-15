# Task 069 — Full repair and release candidate

## Summary

This task completed the D1 backup/restore repair, fixed the remaining confirmed
application defects (D2–D6), added a safe orphaned-upload maintenance mechanism,
and re-verified the inventory application, the simulated LED system and the
browser interface against the final code.

- Baseline (starting) commit: `931e6b61b981125d3d82eb4609d91d167f555b94`
- Branch: `release-candidate/wledger-full-repair` (local only; not pushed)
- Worktree: `.d1-worktree`

## Defects fixed

| ID | Defect | Fix |
| --- | --- | --- |
| D1 | Restore failed in the Docker deployment (mount-point uploads) and recovery metadata could be lost on container recreation. | Already repaired at the baseline (mount-aware generation swap, durable in-mount journal, fail-closed recovery). This task additionally made the cross-process maintenance lock mount-aware so every container sharing the uploads bind mount contends on the same lock file. |
| D2 | `web/tests/ui_only_install.browser.test.js` was stale (waited for a removed `Configure physical strings` button). | Updated the harness to the current UI: controller outputs are configured in the Hardware editor, and the cabinet editor maps those outputs onto the canvas (`Save output paths`). The harness now passes. |
| D3 | Part form category selector threw `URL is not a constructor` on `/parts/new`. | `web/components/part_form.templ` now uses `new window.URL(...)`, so the inline handler no longer resolves `URL` through `with(document)`. |
| D4 | Backup export carried orphaned upload files. | New `internal/uploads` service detects files no part/category/document references and removes them only after re-validating that they are still unreferenced. Exposed as an admin-only `/settings/uploads` page with an audited delete action. |
| D5 | Malformed/undersized images were silently ignored. | `parts.CreatePart`/`UpdatePart` now return `ErrInvalidImage`, mapped to a 400 with a clear message; the part is not created/updated without the selected image. |
| D6 | `web/tests/layout_editor.browser.test.js` was intermittently flaky. | Root cause: the deliberately invalid custom output permutation is rejected by the debounced live pixel preview (`POST /hardware/output-mapping-preview`) as well as by the save, and the error page re-initialises the editor. The assertion now waits for the preview response and asserts on the distinct endpoints that produced a bad request. A second timing-dependent exact count of `/json/info` health-badge pings was replaced with the meaningful invariant (only read-only `/json/info` and `/json/cfg` requests). |

## Changes

| File | Change |
| --- | --- |
| `internal/maintenance/mount.go` | New. Shared mount-point detection (`IsMountPoint`). |
| `internal/maintenance/gate.go` | Mount-aware `LockPath`; the lock lives inside the persistent mount when the uploads path is a mount point. |
| `internal/backup/restore_generation_mount.go` | Delegates mount detection to `maintenance.IsMountPoint`. |
| `internal/parts/service.go` | `ErrInvalidImage`; reject undecodable image uploads. |
| `internal/handler/parts.go` | Map `ErrInvalidImage` to a 400 with a clear message. |
| `internal/uploads/service.go` | New. Orphaned-upload detection and conservative cleanup. |
| `internal/handler/uploads_maintenance.go` | New. Admin upload-maintenance page and audited delete action. |
| `internal/router/router.go` | Register `/settings/uploads` and `/settings/uploads/delete`. |
| `web/pages/upload_maintenance.templ` | New. Upload-maintenance page. |
| `web/pages/settings.templ` | Link to the upload-maintenance page. |
| `web/components/part_form.templ` | `new window.URL(...)` category selector fix. |
| `locales/active.en.json` | New message keys. |
| `web/tests/layout_editor.browser.test.js`, `web/tests/ui_only_install.browser.test.js` | D6 and D2 test corrections. |
| `internal/parts/service_image_test.go`, `internal/uploads/service_test.go`, `internal/handler/uploads_maintenance_test.go`, `internal/maintenance/gate_test.go` | New regression tests. |

## Verification (final code)

- `go build -tags fts5 ./...` — PASS.
- `go vet -tags fts5 ./...` — PASS.
- `go test -tags fts5 -count=1 ./...` — PASS.
- `go test -race -tags fts5 -count=1 ./...` — PASS.
- `npm test` — PASS (grid painter + pixel mapping).
- `node web/tests/layout_editor.browser.test.js` — PASS (5 consecutive runs).
- `node web/tests/physical_first.browser.test.js`, `barcode_scanner.browser.test.js`, `csrf.browser.test.js` — PASS.
- `node web/tests/ui_only_install.browser.test.js` — PASS (5/5 mapping attestations, Locate PASS, no page errors).
- `WLEDGER_TASK067_LOCATE=1 node web/tests/task064_fresh_install.browser.test.js` — PASS (crash recovery, separate-destination restore and multi-process Locate all PASS).
- `WLEDGER_TASK064_INVENTORY_ONLY=1 WLEDGER_TASK065_RESTORE=1 node web/tests/task064_fresh_install.browser.test.js` — PASS.
- `scripts/test-restore-mount.sh` (real Docker bind mount, reproduces `EBUSY`) — PASS.
- `scripts/test-restore-mount-recreate.sh` (real Docker container recreation at the `drain`, `drain-marker`, `fill` and `commit` failpoints) — PASS.
- `gofmt` clean on every changed file; `git diff --check` clean.

## Remaining limitations

- A mount point cannot be swapped in one syscall, so activation on a mount is a
  sequence of atomic per-entry renames; serving consistency is provided by the
  exclusive maintenance gate and recovery consistency by the durable in-mount
  journal, drain marker and in-database generation marker.
- Mount detection relies on Linux `/proc/self/mountinfo`; other platforms fall
  back to the unchanged whole-directory swap.
- Physical WLED output was never enabled and no real controller was contacted.
  Only loopback simulators were used; hardware commissioning remains
  owner-controlled.
- No push, deployment, production/staging database change, main merge, release
  or LED-coordinate conversion was performed.
