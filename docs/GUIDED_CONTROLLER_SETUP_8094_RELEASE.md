# Guided controller setup — deployed to isolated port 8094

## Release

- Application commit: `fa16a5316f075688c14249d7b9f9059c1fe18f5e`
- Parent: `15a8207703041d7002408272a70881cb3e1f3259`
- Published by normal fast-forward to `origin/development/chatgpt-m6-m7`.
- Image: `wledger:guided-8094-fa16a53`
- Image ID: `sha256:fc06df205556d1851b726517cdd87c095a46549dea3dc959f3d68f9ffd231b9c`
- Image built from the clean published Git archive; OCI revision matches the code.
- Live container: `wledger-rc-8094`, healthy, same persistent volumes and port mapping.
- Physical WLED output remains disabled. No real controller was contacted.

## User workflow

Open <http://192.168.1.182:8094/hardware> (or <http://localhost:8094/hardware>)
and click **Add WLED controller**. The old technical modal is removed.

1. Enter name, IP/hostname and port.
2. Optionally select **Discover Controller** for explicit, read-only capability
   discovery, or continue manually when offline.
3. Continue directly into the existing graphical designer with an unsaved draft.
   Add outputs, enter pixel counts and choose unequal row/column layouts visually.
4. **Save controller and layout** commits all three together. Validation/storage
   failure rolls everything back and retains the draft for correction/retry.
5. Existing **Edit Wiring** cards open the same designer. Raw electrical transforms
   and technical connection settings remain Advanced, not initial setup requirements.

Opening Hardware or the designer does not poll controller status automatically.
**Check connection** and **Discover Controller** are explicit read-only actions.
Discovery has administrator/CSRF checks, bounded GET info/cfg requests, size/time
limits and no redirect/proxy forwarding. It never changes WLED state/configuration.
New graphical drafts retain linear electrical addresses; visual serpentine edits
never silently renumber physical pixels. Existing mapping safety gates remain intact.

## Verification

Passed Go build/vet, complete race suite, focused normal Go tests, JS unit tests,
new guided-setup browser, existing designer/layout/physical-first/barcode/CSRF/Test
Centre browsers, and extended fresh-install Locate/SIGKILL/restore/multi-process
acceptance. The latter reports PASS, no findings and no page errors.

New tests cover:

- No controller row during details/discovery/manual continuation.
- Multiple outputs and unequal row/column lengths before the first save.
- Negative draft-ID remapping and existing-designer reopening.
- Offline final save and GET-only simulated discovery.
- Atomic rollback when graphical flag insertion is deliberately rejected.
- Duplicate endpoints/draft identities, bad coverage and authorization/CSRF rejection.
- Existing mapping and pixel-identity preservation through visual edits.

## Backup, copied-data proof and live cutover

Private backups are outside Git under
`.8094-backups/20261017-pre-guided-controller-setup/`; `final-cutover/` is the latest
pre-swap capture. Database/WAL, uploads, logs, previous Compose and container
configuration were captured with only 8094 briefly stopped. SQLite integrity,
foreign keys and SHA-256 checks passed; migration remains 27.

The restored backup booted on the previous image. The candidate was then tested on
that copy in an internal-only Docker network. The actual Hardware Add link, details,
unsaved canvas, multiple outputs, irregular rows, final combined save and Edit Wiring
reopen all passed. Application backup/restore also preserved the graphical document
and raster background. Equivalent UTC timestamp formatting was normalized only on
the restored disposable copy.

A fresh pre-swap backup matched the accepted protected data. Only 8094 was recreated.
Authenticated live browser testing repeated the actual guided Add/Edit flow and
save/reopen with no page errors. Temporary offline preview records/flags and the
maintenance session were removed. All 49 protected application-table fingerprints
matched the cutover backup, including administrator/password hash and settings.
No password was reset and no application restore was performed over live user data.

8090, 8092 and 8093 container identities, images, configurations, persistent mounts
and start times are unchanged (mount lists compared by destination, not JSON order).
No 8091 service was altered. No main merge or production installation access occurred.

## Remaining status

No deployment blocker remains. Mapped-controller electrical rewiring is deliberately
protected; layout-only edits remain available. PNG/JPEG and large-canvas limits are
unchanged. Physical commissioning needs separate authorization; independent
published-source review remains the project acceptance checkpoint.
