# Task 068 — D1 repair: mount-aware backup restore

## Summary

Backup restore now works in the standard Docker deployment, where the uploads
directory is a bind mount. The mount point is never renamed, unmounted or
replaced; instead the uploads generation is activated by swapping the mount's
contents. The default whole-directory swap is unchanged for ordinary uploads
directories. Data-integrity, rollback and crash-recovery guarantees are
preserved and covered by new regression tests, including a real Docker
bind-mount reproduction of the original `EBUSY` failure.

- Baseline (published) commit: `6dc85d3`
- D1 repair implementation commit: `863e2274ca302ac1185c834a474da1e1d643dbf3`
- Branch: `fix/d1-mount-aware-restore` (local only; not pushed — publication
  requires owner approval)

## Root cause

`internal/backup/restore_generation.go:finishGeneration` swapped upload
generations by renaming the live uploads directory aside:

```
renameSynced(uploadsDir, previous)   // os.Rename /wledger/app/uploads -> .../<id>.previous
renameSynced(candidate, uploadsDir)  // activate the extracted candidate
```

`os.Rename` of a mount point returns `EBUSY`. In the standard deployment
`docker-compose.yaml` bind-mounts `./uploads` onto `/wledger/app/uploads` (and
the `Dockerfile` declares it a `VOLUME`), so the first rename failed with
`device or resource busy` and restore was unusable there. The failure is safe
(the database transaction rolls back), but blocking.

## Design

### Mount detection

`isMountPoint(path)` reads `/proc/self/mountinfo` and reports whether the
resolved absolute path is itself a mount target (field 5, with octal-escape
decoding). Bind mounts — including the Docker uploads bind mount — are mount
targets. On systems without `/proc/self/mountinfo` it returns `false`, so the
existing directory-swap protocol is preserved unchanged. Detection is a runtime
property of the deployment; no backup format change is required.

### Mount-aware generation protocol

When the uploads path is a mount point:

- The candidate generation is extracted **inside the mount**, under the hidden
  control directory `<uploads>/.wledger-restore/<id>.candidate`, so every
  activation rename stays on one filesystem (a candidate staged outside the
  mount would `EXDEV`).
- The previous generation is `<uploads>/.wledger-restore/<id>.previous`.
- Activation swaps the mount's **non-hidden top-level entries** in two phases:
  1. **Drain**: rename every non-hidden live entry into `previous`, then write
     and `fsync` a durable `<previous>/.drained` marker.
  2. **Fill**: rename every candidate entry into the live namespace, then remove
     the empty candidate directory.
- Hidden entries (names beginning with `.`) are the restore control plane —
  the staging directory, the maintenance lock, and operator recovery
  directories (`.uploads_bak_*`) — and are never swapped, so operator recovery
  data is never moved or deleted.
- The unchanged durable journal still lives in the sibling control directory
  (so the maintenance middleware's fail-closed check keeps working), and the
  committed generation is still written in the same SQLite transaction as the
  restored rows.

The default whole-directory swap is used verbatim whenever the uploads path is
not a mount point.

### Achievable atomicity guarantees

- **SQLite**: full transactional atomicity. The restored rows, the generation
  marker and the physical-graph rows commit or roll back together. A restore
  does not partially apply.
- **Uploads filesystem on a mount**: a mount point cannot be replaced in a
  single syscall, so activation is a sequence of per-entry renames rather than
  one directory rename. Each rename is atomic; the *set* is not. This is the
  honest limit of the approach, and the two properties below compensate for it.

### Preventing mixed-generation serving during activation

The restore holds the exclusive maintenance gate (`Gate.WriteLock`) for the
whole operation. The gate middleware is registered globally, so every other
request (including `/uploads/*`) takes a shared lock and therefore blocks until
the swap completes. No request — filesystem or database — can observe a
half-swapped namespace, and the SQLite write reservation (`BEGIN IMMEDIATE`)
excludes concurrent writers. In-process restore calls are additionally
serialised by `restoreMu`.

### Recovery after interruption

At startup, before any service, migration, worker or route, `RecoverStartup`
takes the exclusive gate and reads the durable journal:

- If the database generation marker equals the journal generation, the restore
  committed: roll forward idempotently (complete the drain/fill if needed),
  verify the active generation, retire the journal, and delete the replaced
  generation.
- Otherwise the restore did not commit: roll back. The durable `.drained` marker
  makes the phase unambiguous — without it the live directory holds only
  previous-generation entries (the fill has not overwritten anything, so moving
  `previous` back is enough); with it the live directory holds only
  candidate-generation entries (move them back into the candidate staging
  directory, then move `previous` back). The result is exactly the previous
  generation.

Because the database commit happens *after* the fill completes, a committed
restore always has a complete fill; a partial swap is always uncommitted and is
rolled back. Both directions are exercised by tests below, including recovery
from a partial drain and from a partial fill.

## Failure reproduction

`/proc/self/mountinfo` confirms the uploads path is a mount point, and
`os.Rename(uploadsDir, sibling)` fails with `EBUSY`:

```
reproduced D1: rename(/wledger/app/uploads) ->
    rename /wledger/app/uploads /wledger/app/wledger-ebusy-probe:
    device or resource busy
```

`scripts/test-restore-mount.sh` produces this against a real Docker bind mount
(the same `-v …:/wledger/app/uploads` layout as `docker-compose.yaml`) and then
verifies the mount-aware restore succeeds on the very same directory.

## Changes

| File | Change |
| --- | --- |
| `internal/backup/restore_generation_mount.go` | New. Mount detection and the mount-aware drain/fill/rollback/cleanup protocol. |
| `internal/backup/restore_generation.go` | Dispatch `finishGeneration`/`rollbackGeneration` and the post-commit cleanup to the mount-aware path when the uploads path is a mount point. Default path unchanged. |
| `internal/backup/service.go` | Stage the candidate inside the mount when the uploads path is a mount point; clear abandoned in-mount generation directories after recovery. |
| `internal/backup/restore_mount_test.go` | New. Mount-mode unit/regression tests and the real bind-mount test. |
| `scripts/test-restore-mount.sh` | New. Docker bind-mount harness for the real-mount regression. |

No application data, schema, migration, backup format, deployment file, port or
running instance was changed.

## Tests and verification

Environment: host Go 1.26.0 toolchain (CGO, gcc) and a disposable
`golang:1.26.9` container; Chromium `/bin/chromium`; Node 22.23.2.

- `go test -tags fts5 -count=1 ./...` — PASS (all packages).
- `go test -race -tags fts5 -count=1 ./...` — PASS, no data races.
- `go vet -tags fts5 ./...` — PASS.
- `go build -tags fts5 ./cmd/server` and `./cmd/mcp-server` — PASS.
- `gofmt -l` clean; `git diff --check` clean.
- Mount-mode tests (`-run TestRestore_MountMode`) — PASS:
  `SuccessSwapsContentsWithoutRenamingMountPoint`,
  `InvalidBackupLeavesUploadsUntouched`,
  `InjectedActivationFailureRollsBack`,
  `PreservesRecoveryDataAndCleansStaging`,
  `RecoversInterruptedDrain`, `RecoversInterruptedFill`,
  `RollsForwardCommittedRestore`,
  `RestoresAndExportsCategoryPartAndDocAssets`,
  `MatchesNonMountModeOutcome`.
- Real Docker bind-mount regression (`scripts/test-restore-mount.sh`) — PASS,
  reproducing `EBUSY` and completing the mount-aware restore on the same mount.
- `npm test` (grid painter + pixel mapping) — PASS.
- Browser restore acceptance
  (`WLEDGER_TASK064_INVENTORY_ONLY=1 WLEDGER_TASK065_RESTORE=1 … node
  web/tests/task064_fresh_install.browser.test.js`) — `"result":"PASS"`,
  `destinationPageErrors: []`, `wledCommandsDuringRestore: 0`. Physical output
  disabled; WLED endpoints were test-local loopback simulators only.

## Follow-up tasks (separate; not part of the D1 commit)

- **D2** — `web/tests/ui_only_install.browser.test.js` is stale: it waits for a
  `Configure physical strings` button that no longer exists. Test-harness only;
  superseded by `layout_editor`/`task064`/`physical_first` and unreferenced by
  any `package.json` script. Fix or delete.
- **D3** — Part form category selector throws a client-side error on
  `/parts/new`: `web/components/part_form.templ` uses
  `onchange="const u = new URL(location.href); …"`, and inline handlers resolve
  `URL` through `with(document)`, where `document.URL` is a string, so
  `new URL(...)` throws. Category-specific fields load only after a manual
  reload. Pre-existing.
- **D6** — `web/tests/layout_editor.browser.test.js` is intermittently flaky: it
  asserts an exact count of `400` console entries
  (`invalidConfigurationResponses.length === 1`), which a timing-dependent extra
  entry breaks. Product behaviour is unaffected. Test-harness only.

(D4 and D5 from the `6dc85d3` acceptance remain informational and out of scope.)

## Remaining limitations

- A mount point cannot be swapped in one syscall, so activation on a mount is a
  sequence of atomic per-entry renames rather than a single atomic directory
  rename. Serving consistency during the swap is provided by the exclusive
  maintenance gate, not by a single syscall; recovery consistency is provided
  by the durable journal, the in-mount drain marker and the in-database
  generation marker.
- Mount detection relies on Linux `/proc/self/mountinfo`. On other platforms the
  behaviour falls back to the unchanged directory swap (the Docker deployment
  this repair targets is Linux).
- The restore journal remains in the sibling control directory (outside the
  mount) so the existing middleware fail-closed check is unchanged. A container
  *recreation* part-way through activation (not a restart) can therefore lose
  the journal while the mount persists; this pre-dates D1 and is not a
  regression. The mount-aware protocol never deletes operator recovery
  directories.
- No physical WLED output was enabled and no real controller was contacted.

## Guardrails observed

- Work performed in the isolated worktree `.d1-worktree` from `6dc85d3`; the
  running `8093` review instance and its data were not touched.
- Ports `8090`–`8093` and their containers/images/volumes/databases were not
  modified.
- `WLEDGER_PHYSICAL_OUTPUT_ENABLED` remained disabled; only loopback simulators
  were used.
- No push, deployment, production/staging database change, main merge, release
  or LED-coordinate conversion was performed.
