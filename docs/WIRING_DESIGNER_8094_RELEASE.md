# Graphical Wiring Designer — isolated 8094 release evidence

## Published application and deployment

- Application commit: `39f4d1410195dcf99bb3b9514eb22896b91e09b5`
- Parent: `f54649312342a9a9c8dc2927470bdbe7660fa9f9`
- Published branch: `origin/development/chatgpt-m6-m7`
- Publication was a normal fast-forward from `b235bfd`; no force push or main merge.
- Application image: `wledger:designer-8094-39f4d14`
- Image ID: `sha256:dd9f46fb89b6851692b6d3ec9c3a392a49846dca520225f8f85005386941c180`
- OCI revision label matches the application commit.
- Built from a clean `git archive` of the published commit, excluding untracked
  operational files, backup data, test debris and credentials.
- Container: `wledger-rc-8094`; same named data/uploads/logs volumes; port 8094.
- Physical output: **disabled** (`WLEDGER_PHYSICAL_OUTPUT_ENABLED=false`).
- Browser: <http://localhost:8094/hardware> or <http://192.168.1.182:8094/hardware>.

This image contains the repaired inventory/backup application, LED Test Centre
and graphical Wiring Designer together. No schema migration was added by the
new designer; startup verified the existing migration 27 database successfully.

## Source verification

Release gates passed: Go build/vet, complete race suite, JavaScript tests and the
isolated graphical-designer browser acceptance. Prior implementation gates also
passed the complete normal Go suite, layout/physical-first/barcode/CSRF/Test Centre
browsers and inventory lifecycle with separate-app restore. Whitespace checks passed.
The feature commit contains 31 scoped source/test/generated/documentation files;
private operational scripts, Compose files and backups were not staged.

## Recoverable backup and copied-data acceptance

Only 8094 was briefly stopped for each consistent capture. A copied SQLite/WAL
snapshot was consolidated using SQLite's backup API, with matching uploads, logs,
previous Compose configuration and container configuration. Integrity, foreign
keys and SHA-256 checks passed. Private material is retained outside Git under
`.8094-backups/20261017-pre-wiring-designer/`; `final-cutover/` is the latest
pre-swap recovery snapshot and `RECOVERY.md` describes controlled rollback.

The initial backup was restored to an isolated copy and booted on the previous
image, proving it recoverable. That copy was then upgraded to the actual candidate
image on an internal-only Docker network. Browser acceptance passed:

- Existing administrator authentication via a short-lived maintenance session;
  no account password reset or replacement.
- Hardware → Graphical Wiring Designer navigation.
- Irregular column layout and split sections, embedded raster background,
  graphical save and reopen with no page errors.
- Application backup download and restore of the complete graphical document.
- All 49 protected application tables preserved, including administrator/password
  hash, settings, inventory and mappings. Restore normalized only equivalent UTC
  timestamp formatting on the disposable copy.

No real controller was used. Temporary preview controllers used only loopback at
an unused port, with physical output disabled. The copied-data browser blocked
external browser requests and supplied Alpine locally.

## Live cutover and final verification

A fresh final backup was taken immediately before the swap. Its protected data
matched the accepted initial snapshot. Only the 8094 Compose service was recreated,
without rebuilding or removing persistent volumes. The new container is healthy;
loopback and LAN login URLs return HTTP 200.

Authenticated live browser acceptance passed layout creation/save/reopen through
Hardware with no page errors and physical output disabled. Temporary offline
preview records, graphical flags and maintenance sessions were then removed.
Live SQLite integrity/foreign-key checks passed and all 49 protected application
tables matched the final-cutover fingerprints exactly. Application backup/restore
was intentionally exercised only on the copy, not over live user data.

The 8090, 8092 and 8093 containers were checked against their initial inspections:
container IDs, images, configurations, persistent mounts and start times are
unchanged. No 8091 service was altered. No production installation was accessed.

## Remaining limitations and use

The installation currently has no configured controllers. Add a controller/output
in Hardware, then choose **Graphical Wiring Designer** on its card. The temporary
verification controller was deliberately removed rather than leaving demo data.

Electrical edits of already-mapped controllers are conservatively blocked; visual
arrangement remains available. Backgrounds accept bounded PNG/JPEG data, and large
outputs sample displayed pixels. Physical commissioning needs separate approval.
HTTP/insecure cookies remain the existing trusted-LAN configuration; use HTTPS
before exposure beyond that LAN. Independent published-source review remains the
project's acceptance checkpoint, not a deployment-health blocker.
