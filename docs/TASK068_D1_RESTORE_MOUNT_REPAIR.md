# Task 068 — D1 repair: mount-aware backup restore

## Summary

Backup restore now works in the standard Docker deployment, where the uploads
directory is a bind mount. The mount point is never renamed, unmounted or
replaced; instead the uploads generation is activated by swapping the mount's
contents. The default whole-directory swap is unchanged for ordinary uploads
directories. Data-integrity, rollback and crash-recovery guarantees are
preserved and covered by new regression tests, including a real Docker
bind-mount reproduction of the original `EBUSY` failure.

A follow-up repair (see “D1 follow-up repair” below) moves the durable restore
journal inside the persistent uploads mount, so recovery no longer depends on
any file that disappears when Docker recreates the container, and adds a real
Docker container-recreation regression harness.

- Baseline (published) commit: `6dc85d3`
- D1 repair implementation commit: `863e2274ca302ac1185c834a474da1e1d643dbf3`
- D1 follow-up repair commit: `e8ab2a2` (durable recovery metadata inside the
  persistent uploads mount)
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

## D1 follow-up repair — durable recovery metadata inside the persistent mount

### Identified gap

The mount-aware protocol kept the durable restore journal in the sibling control
directory, `maintenance.ControlDir(uploadsDir)` =
`<parent-of-uploads>/.wledger-restore-control-<hash>`. In the standard Docker
deployment the uploads path is `/wledger/app/uploads` and its parent
`/wledger/app` is **not** a persistent volume (only `./data`, `./uploads` and
`./logs` are bind-mounted), so the journal lived on the container filesystem.
`docker restart` kept it, but `docker compose up --force-recreate` (or any
container recreation) discarded it while the uploads bind mount persisted.

Recovery metadata locations before this repair:

| Metadata | Location | Survives container recreation? |
| --- | --- | --- |
| Restore journal | sibling control dir (`/wledger/app/.wledger-restore-control-<hash>`) | **No — the gap** |
| Maintenance lock | sibling control dir | No (recreated on demand; not recovery state) |
| Candidate / previous generation dirs | in-mount `.wledger-restore/<id>.{candidate,previous}` | Yes |
| Durable drain marker | in-mount `<previous>/.drained` | Yes |
| Committed generation marker | SQLite database (`./data`) | Yes |

A container recreation part-way through activation therefore lost the journal
while the mount kept a half-swapped namespace, and startup recovery had nothing
to act on: the live namespace could be served in a mixed generation.

### Repair

1. **The journal now lives inside the persistent mount.** `journalPath` writes
the journal to `<uploads>/.wledger-restore/.wledger-restore-journal.json` when
the uploads path is a mount point (the same hidden control directory that
already holds the generation staging), and to the sibling control directory
otherwise. The journal is written and fsynced before any live-namespace
mutation, so it is durable before the drain begins.
2. **The in-mount generation directories are self-describing recovery
metadata.** If the journal is missing (lost, or removed by an operator) but
durable generation staging directories exist inside the mount, startup recovery
reconstructs the pending generation from the directory names and the
in-database committed-generation marker. It fails closed when the on-disk state
is ambiguous (more than one generation ID, or an unrecognised entry).
3. **The fail-closed middleware covers both journal locations.** `Gate` now
remembers its uploads directory and `hasPendingRestore` checks the in-mount
journal as well as the sibling one, so a pending restore blocks every request
regardless of where the journal lives.
4. **The uploads route never serves the control plane.** `HandleUploads` refuses
any path with a hidden segment, so the in-mount journal and generation
directories are unreachable over HTTP.
5. **Cleanup is journal-aware.** Recovery removes the now-empty in-mount control
directory only after the journal has been retired, so no restore artifact is
left in the served uploads namespace.

The default whole-directory swap for an ordinary (non-mount) uploads directory
is unchanged.

### Failure scenarios

| # | Scenario | Outcome |
| --- | --- | --- |
| 1 | Container killed during the drain phase | Exact recovery of the previous generation (journal in-mount, or reconstructed from the durable generation directories). |
| 2 | Container killed during the fill phase | Exact recovery of the previous generation (the durable `.drained` marker distinguishes the phase). |
| 3 | Container recreated with the same uploads bind mount and a fresh container filesystem | Exact recovery: the in-mount journal and generation directories persist, so recovery runs exactly as after a restart. |
| 4 | Database committed, then container recreated before cleanup | Exact completion of the committed new generation; the replaced generation is retired. |
| 5 | Failure after moving some, but not all, top-level uploads entries | Exact recovery of the previous generation (partial drain or partial fill). |
| 6 | Missing, corrupted or contradictory journal / generation markers | Missing journal with unambiguous durable generations: exact recovery. Ambiguous generations, a corrupted journal, or a journal that contradicts the on-disk generation: fail-closed startup that preserves every file and requires explicit recovery. |

### Changes (follow-up repair)

| File | Change |
| --- | --- |
| `internal/maintenance/gate.go` | Add `InMountControlDir`/`JournalPaths`; the fail-closed check now covers the in-mount journal. |
| `internal/backup/restore_generation.go` | Durable in-mount `journalPath`; dual-location journal read/retire; reconstruct a missing journal from the durable in-mount generation directories; fail closed on ambiguity or contradiction. |
| `internal/backup/restore_generation_mount.go` | Share the control-directory name with `maintenance`; add a deterministic activation failpoint. |
| `internal/handler/uploads.go` | Refuse to serve any hidden path. |
| `internal/backup/restore_mount_persistence_test.go` | New. Deterministic scenario regression tests. |
| `internal/backup/restore_mount_recreate_test.go` | New. In-container half of the real container-recreation harness. |
| `internal/maintenance/gate_test.go`, `internal/router/router_uploads_test.go` | New fail-closed and hidden-path tests. |
| `scripts/test-restore-mount-recreate.sh` | New. Real Docker container-recreation harness. |

### Tests and verification (follow-up repair)

- Deterministic unit/regression tests (`internal/backup/restore_mount_persistence_test.go`):
  `JournalLivesInsideMountDuringActivation`,
  `RecoversInterruptedDrainWithoutJournal`,
  `RecoversInterruptedFillWithoutJournal`, `RollsForwardCommittedWithoutJournal`,
  `JournalSurvivesContainerRecreation`, `MissingJournalAmbiguousFailsClosed`,
  `CorruptedJournalFailsClosed`, `ContradictoryJournalFailsClosed`. Each was
  confirmed to fail against the pre-repair behaviour.
- `internal/maintenance/gate_test.go`: `TestGateMiddlewareFailsClosedForInMountJournal`.
- `internal/router/router_uploads_test.go`: `TestUploadsHandlerRefusesHiddenControlPlane`.
- Real Docker container recreation (`scripts/test-restore-mount-recreate.sh`):
  for each of the `drain`, `drain-marker`, `fill` and `commit` failpoints, one
  container performs a restore that is killed at the failpoint and a second,
  freshly created container over the same persistent `uploads` and `data` bind
  mounts runs startup recovery and asserts the outcome. Confirmed to fail
  against the pre-repair behaviour.
- `go test -tags fts5 -count=1 ./...` — PASS.
- `go test -race -tags fts5 -count=1 ./internal/backup/ ./internal/maintenance/ ./internal/router/ ./internal/handler/` — PASS.
- `go vet -tags fts5 ./...` — PASS; `gofmt` clean on changed files;
  `git diff --check` clean.
- `scripts/test-restore-mount.sh` (original D1 EBUSY regression) — PASS.

## Remaining limitations

- A mount point cannot be swapped in one syscall, so activation on a mount is a
  sequence of atomic per-entry renames rather than a single atomic directory
  rename. Serving consistency during the swap is provided by the exclusive
  maintenance gate, not by a single syscall; recovery consistency is provided
  by the durable in-mount journal, the in-mount drain marker and the
  in-database generation marker.
- Mount detection relies on Linux `/proc/self/mountinfo`. On other platforms the
  behaviour falls back to the unchanged directory swap (the Docker deployment
  this repair targets is Linux).
- The maintenance lock file remains in the sibling control directory (outside
  the mount). It is not recovery state: it is recreated on demand and a
  recreated container simply creates a new one. The durable restore journal now
  lives inside the mount, so recovery no longer depends on any file that
  disappears when Docker recreates the container.
- Reconstruction of a missing journal is performed only for a mounted uploads
  directory. For an ordinary uploads directory the journal and the generation
  staging share the container filesystem and are lost together, so there is
  nothing to reconstruct.
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
