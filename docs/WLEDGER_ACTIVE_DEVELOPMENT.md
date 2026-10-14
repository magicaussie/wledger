# WLEDger Active Development

## Baseline and authorization

- Authorized branch: `development/chatgpt-m6-m7`
- Published baseline: `46aec54fa6a97c59f34776add009568b18991783`
- Owner authorized continued implementation, testing, and publication of the scoped feature-completion task, including tested intermediate milestones.
- Production and staging installations/databases and real WLED controllers remain out of scope. Physical output must remain disabled by default. `.task055b-staging/` and the protected handover/audit documents are not to be inspected or modified.

## Architecture invariants

- Inventory identity and quantities remain independent of storage layout, numbering, mounting, and LED mapping.
- Physical cabinets/drawers/bins are distinct from legacy electrical containers/segments.
- Controller output topology, cabinet path geometry, and drawer pixel allocations must remain separate concerns.
- A drawer may own multiple non-contiguous pixel ranges; mappings must resolve to exact controller-local pixels and reject conflicts.
- WLED output is guarded off by default; all protocol tests use isolated loopback simulators.

## Completed checkpoint — category and general product CSV integration

The preserved uncommitted category/product work and the measurement-unit continuation now include:

- Hierarchical reusable categories, typed category attributes, product assignments, validation/search, images, labels/scanner navigation, and backup/restore compatibility.
- General product descriptors and purchasing information, selectable dimensions/weight units, search, CSV import/export, and backup/restore.
- Human-readable product CSV now carries category names and JSON-encoded typed specifications. Import resolves existing category/attribute names, enforces required/type/options/range validation, and saves the product/category/specification records atomically.
- Existing CSVs without category columns remain valid; older product records and backups default measurement units to `mm`/`g`.

Verification for this checkpoint: `go test -tags fts5 ./...`, `go test -race -tags fts5 ./internal/importer ./internal/handler ./internal/backup ./internal/db`, `go vet -tags fts5 ./...`, server/MCP builds, `npm test`, and the fresh-install browser scenario completed without command failures. The fresh-install script reports **PARTIAL** for the broader app scenario; category UI lifecycle, unit selectors, and mock Locate/Stop paths were exercised. Backup CSV round-trip tests cover category/specification export and restore. `govulncheck`, Trivy, and Grype are not installed. Repeat all final gates before final publication.

## Remaining work

1. Put controller type, connection and output/string configuration (pixel counts, differing lengths, order, direction/reversal, serpentine/custom patterns and preview) in Hardware add/edit with persistent, validated settings.
2. Simplify cabinet setup to controller assignment, capacity display and physical layout; migrate/retain existing valid mappings safely and remove duplicated hardware controls from cabinet layout.
3. Complete individual drawer allocation UI for multiple/non-contiguous ranges, output/controller selection where meaningful, availability/conflict feedback and visual preview.
4. Close any remaining Locate/Stop/Global Off and product/bin selection gaps; verify exact protocol payloads, pixel framebuffer changes, concurrent ownership and restart/disconnection recovery only against isolated simulators.
5. Run full Go/race/vet/build, JS/browser, migration/backup/CSV, controller protocol/concurrency, security, and disposable Docker build/smoke gates. Do not publish an incomplete task.

## Current Git safety

The original working-tree changes to `AGENTS.md`, `docs/OMO_HANDOVER.md`, `docs/TASK056_FUNCTIONAL_AUDIT.md`, and `.task055b-staging/` are preserved and excluded from task commits. Stage only reviewed source, generated files required by changed templates/queries, tests, migrations, and this task-specific handover. Before each push, verify the authorized branch, remote SHA/alignment, and staged file list; never force-push.
