# WLEDger OmO Development Handover

## Active session and authorization

This is the single active development handover document. `AGENTS.md` defines
development policy; the root `ROADMAP.md` records product requirements and design
updates. Historical handovers are references, not active instructions.

Current task: Task 045, M1 - Read-only Compatibility Baseline.

Status: M1 accepted by the owner after the corrections, Sol review and passing
automated checks. The owner authorized staging only the three approved files
and the local commit `feat: add read-only compatibility baseline`.
M2 scope preparation is authorized; M2 implementation remains unauthorized.

The user approved changes only to `internal/hardware/compatibility.go`,
`internal/hardware/compatibility_test.go` and this handover document. The helper
must not change existing runtime behavior or write to databases, audit logs,
files, the network or hardware. Isolated regression tests may initialize the
existing schema in temporary fixtures. No new SQL migrations or schema changes,
other file changes, application startup, deployment, production access, WLED
access, pushes or merges are authorized. The accepted M1 local commit is the
only current commit authorization.

## Git baseline

| Item | Baseline |
| --- | --- |
| Worktree | `/home/spetchal/Code/wledger-omo` |
| Branch | `transition/omo` |
| Starting HEAD | `05c0d5b021c8bed7a87b6f787dc0f337a7b50110` |
| Local `origin/main` reference at assessment | `05c0d5b021c8bed7a87b6f787dc0f337a7b50110` |
| Origin fetch/push URL | `https://github.com/magicaussie/wledger` |
| Local `origin/experiment/ai-handoff` reference | `4c390e8dbf7858b2801e9936e367447ba1705921` |
| Working tree before transition | Clean |
| Current reviewed local HEAD / M1 starting point | `0fc00cfb45cf29634515ab9e2fdad741cd55437d` |

These are locally inspected Git references. No fetch was performed and current
GitHub synchronization was not independently verified. The documentation
transition was independently reviewed and committed locally with explicit
authorization; it was not pushed.

## Responsibilities and delegation

GPT-6.1 Sol retains lead responsibility for important architectural, preservation
and security decisions. DeepSeek V4.1 Flash handles routine implementation and
tests; Sol is reserved for the important lead tasks, not routine implementation.

DeepSeek V4.1 Flash is the automatic delegated implementation worker for bounded,
routine coding tasks. Sol delegates suitable approved work through OmO without
asking the user to approve each routine delegation.

Every delegation must specify scope, permitted files, acceptance criteria,
tests and prohibited operations. DeepSeek returns changed files, implementation
evidence, test results, failures, unrun checks and uncertainties. Sol independently
reviews actual source changes and verifies tests before acceptance.

The owner's streamlined workflow, effective 2026-10-11, requires one Sol review
of delegated code followed by automated checks and ordinary in-scope corrections.
Do not create separate audit tasks or repeated review/approval cycles. Escalate
only architectural decisions, potential data loss, security issues or changes
outside the authorized scope. Keep reports concise: changes, test results,
blockers and next milestone. After M1 passes, present the proposed commit and
wait for authorization before committing or starting M2.

The documentation-only transition needed no implementation worker. M1 is
delegated through OmO to DeepSeek, with the two new Go files as its complete
edit scope; Sol owns this handover and independent review. Delegation never
grants commit, push, deployment, production database or WLED authorization.

## Architecture baseline and invariants

The existing application uses Go, SQLite/FTS5, Goose migrations, sqlc, templ,
HTMX, Alpine.js and WLED. The current storage model is controllers, containers,
bins, parts and part assignments, with walls as display groupings.

Confirmed constraints from the user and the root roadmap, including v0.2-v0.5:

- Legacy containers represent electrical strings/segments, not physical drawers.
- The owner has 69 physical drawers. Historical production evidence reports
  68 mapped database bins and two electrical containers. Their correspondence
  remains unresolved; do not invent a missing mapping or equate records by count.
- The product must support arbitrary cabinet sizes, drawers, bins, LED strings
  and wiring paths. Do not hardcode 69 drawers or a fixed cabinet topology.
- The current product concept assigns one WLED controller per cabinet, with
  multiple strings and multiple disjoint LED ranges per drawer.
- Physical drawer/bin geometry, LED placement and mounting, wiring visualization
  and electrical assignments are separate concepts.
- Mounting may belong to the fixed cabinet frame or moving drawer, including
  side, backlit, perimeter and custom placements.
- Wiring must support horizontal, vertical, serpentine, same-direction and
  arbitrary custom paths; display order is not electrical order.
- Stable identities are separate from editable labels, numbering, ordering,
  geometry and LED mappings. Layout changes must preserve inventory identities
  and quantities.
- `part_assignments` already supports multiple part types per bin and quantities
  per location. Preserve this source of truth.
- Removal of occupied storage requires an explicit relocation, archive or cancel
  workflow, not implicit cascading inventory changes.
- Opening a page or editor must not activate LEDs. Hardware tests require
  explicit authorization.

## Decisions and historical evidence

The takeover assessment and documentation transition are complete. Task 045
architecture planning was reviewed; its physical/electrical separation, stable
inventory identities and compatibility direction were accepted. The user has
authorized implementation of M1 only, not the cabinet redesign or migrations.

Preserve `experiment/ai-handoff` and its Task 044 / Sequence 77 record unchanged
as unapproved archival evidence:

- Task 044: read-only architecture and data audit for the cabinet/drawer/bin
  redesign.
- Sequence 76: ChatGPT request to DeepSeek, status `REQUESTED`.
- Sequence 77: DeepSeek response to ChatGPT, status `AWAITING_REVIEW`.
- Historical audited source: `8922670c148dfde836a1fcc93b770fe6e00c6b29`.
- The handover branch used a frozen application tree at
  `63016f4e75134ae0675934818fb77f53dd463800`; it is not the implementation baseline.

Sequence 77's proposal to reuse containers as physical drawers is superseded
by the later roadmap requirements. Its proposed migrations and implementation
slice are not approved. Do not resume the numbered handover protocol.

The historical claim that the drawer page automatically illuminates on load
is stale: the assessed source uses explicit manual Locate and has a regression
assertion in `internal/handler/drawers_test.go`.

## Risks and unresolved design decisions

- Reconcile the 68 legacy mapped bins with the 69 physical drawers using approved
  evidence before designing a production backfill.
- Define physical string/output versus WLED segment relationships and index
  semantics explicitly; do not assume they are interchangeable.
- Current containers and bins each support one contiguous allocation, not the
  required independent physical drawers with multiple LED ranges.
- `internal/hardware/service.go` matches bins by grid coordinates during saves.
  The new editors need stable-ID updates so moving geometry cannot replace
  inventory location identity.
- Decide LED overlap policy, multi-location Locate selection, timeout/stop
  behavior, geometry conventions and compatibility/rollback contracts.
- Application startup runs schema migrations and hardware data initialization.
  Starting the server is not a read-only inspection action.
- Historical production host information identifies `192.168.1.108`; the
  workstation Docker instance is unrelated. Neither was accessed. Historical
  production permissions do not carry into this session.
- `docs/roadmap.md` contains stale backlog claims. Use current source and the
  newer root `ROADMAP.md` when reconciling requirements.

## Historical transition verification evidence

The takeover assessment read the complete root roadmap, root development policy,
requested build/configuration/CI files, relevant schema and query sources,
application paths and representative tests. No nested `AGENTS.md` was found.

There are 92 Go test files in the assessed tree. Tests were inspected, not run;
several execute migrations and create files. No build, generators, application,
database migration or hardware operation was run for this documentation task.

Transition verification results:

- `git status --short`: exit 0; only ` M AGENTS.md` and
  `?? docs/OMO_HANDOVER.md`.
- `git diff --check`: exit 0; no whitespace errors.
- `git diff -- AGENTS.md`: exit 0; reviewed an append-only diff preserving
  every existing instruction.
- `git diff --no-index /dev/null docs/OMO_HANDOVER.md`: expected exit 1;
  reviewed the entire new document.

No check failed. The expected no-index exit code 1 indicates a new-file diff,
not a verification failure. The same checks are repeated after recording this
evidence, and final results accompany the review handoff.

CI verification for later approved coding work includes generated Go and CSS
consistency, `go build -tags fts5 ./...`, `go vet -tags fts5 ./...` and
`go test -tags fts5 -count=1 ./...`. These were not run in this transition.

## Task 045 implementation boundary

**Task 045 - Flexible Cabinet and LED Database Architecture**

Status: **architecture planning complete; M1 only authorized**.

Proposed scope: define the flexible cabinet/drawer/bin and LED architecture,
stable identity relationships, physical mounting and electrical addressing
separation, and evidence-based migration/compatibility strategy.

The finalized plan was delivered in the conversation. Exclusive controller
ownership, physical LED overlap prevention, multiple drawer ranges, independent
mounting/routes, preservation of stock and explicit legacy reconciliation are
confirmed requirements. M1 establishes preservation evidence without adding
storage entities, changing existing code paths or executing hardware actions.

## M1 implementation and review evidence

- Authorized files: `internal/hardware/compatibility.go`,
  `internal/hardware/compatibility_test.go`, `docs/OMO_HANDOVER.md`.
- Worker task: `st_01a126d4`, OmO `deep-low`, configured route
  `openrouter/deepseek/deepseek-v4.1-flash`, medium reasoning. OmO task metadata
  confirms the resolved model/provider, and the completed worker reported
  `PI_MODEL=deepseek/deepseek-v4.1-flash`, `PI_PROVIDER=openrouter`.
- Correction task: `st_01a12845`, the same configured model/provider and category,
  implemented the three diagnostic fixes and four new regression tests. Sol
  reviewed the returned changes once and directly tightened IPv6 authority and
  port-range validation before running the final automated gate.
- Worker scope: the snapshot helper and regression tests only. Sol independently
  inspected both files, reviewed the worker's original 15-test result, corrected
  in-scope defects directly and ran the final checks below.
- Implemented behavior: one consistent transaction; complete raw controller,
  container, bin, part, assignment and flag values; deterministic versioned
  inventory/mapping/target fingerprints; unchanged legacy target semantics;
  structured unresolved/invalid/unsafe diagnostics; errors for incomplete
  reads; no writes or external operations.
- Changed Go files: new `internal/hardware/compatibility.go` and
  `internal/hardware/compatibility_test.go`. This handover is the
  only changed existing file. No existing runtime hook, SQL, generated file,
  configuration or pre-existing test was changed.
- Fixture policy: temporary isolated SQLite databases and the existing schema
  only; recording or database wrappers must preserve integration behavior.
  The concurrency test uses a WAL database, a real writer, exact hook events
  and bounded contexts; there are no sleeps or polling. The no-write test uses
  SQLite query-only mode on a single fixture connection as well as before/after
  row, flag and audit evidence.

### Independent review findings and corrections

Sol identified and corrected the following before accepting M1:

- Target fingerprints previously included raw widths and stored coordinate
  representations. They now hash effective per-bin command/refusal intent;
  equivalent segment/container-relative representations and legacy width
  clamps keep the target digest while changing the raw mapping digest.
- Empty query results are canonicalized identically for nil and empty slices,
  without changing any row values or SQL NULL validity.
- The coordinate guard now precedes missing-parent checks, matching bin/part
  Locate behavior. Broken relationships remain separately reported; a missing
  controller is not reported as a resolved target.
- Negative allocation counts, invalid starts, negative segment IDs and negative
  controller capacities are reported without repairing the captured values.
- Invalid UTF-8 is rejected before hashing rather than normalized by JSON.
  Non-finite numeric serialization errors also return a zero baseline.
- Regression coverage now includes every snapshot getter's failure path, real
  SQLite serialization failure, equivalent target representations, empty row
  sets, malformed text, guard ordering and invalid allocation metadata.
- Corrupt-fixture helpers restore foreign-key enforcement before returning
  connections to the pool.
- Overlapping bin spans and positive valid allocations now produce deterministic
  paired-ID diagnostics, grouped by controller and segment. All overlapping
  pairs, including nested ranges, are reported; adjacent ranges and independent
  controllers/segments are not. Stored allocations and target intent are unchanged.
- Missing or syntactically unusable controller endpoints now produce warnings
  without network access or address normalization. Valid hostnames, IPv4,
  bracketed IPv6 and IPv6 zones are preserved; malformed literals and out-of-range
  ports are reported. The legacy stored Port remains excluded from wire targeting.
- Assignments referencing missing parts or non-NULL missing bins now produce
  per-assignment diagnostics. Legitimate NULL locations and zero quantities
  remain intact. Corrupt rows are preserved rather than hidden by joins or repaired.
- Four new regression tests cover overlaps, endpoint forms and dangling
  assignments, including query-only capture evidence. Diagnostics do not enter
  the fingerprints; the capture, legacy resolution and fingerprint functions
  remain unchanged.

No blocking review findings remain. Source inspection confirms the helper only
calls read queries inside one `ExecTx`; it has no audit, file, network or WLED
calls, and returned snapshots/fingerprint functions do not mutate stored rows.

### Final verification results

Toolchain: existing `/home/spetchal/.local/go-sdk/go/bin`, Go 1.25.5 linux/amd64.
The SDK directory was added only to command/process PATH; no installation or
configuration file change was required. `CGO_ENABLED=1` was used for Go checks.

| Check | Actual result |
| --- | --- |
| `gofmt -d internal/hardware/compatibility.go internal/hardware/compatibility_test.go` | Exit 0, empty diff |
| `go test -race -tags fts5 -count=1 -run '^TestCompatibility' -v ./internal/hardware` | Exit 0; all 24 top-level regression tests passed, no race reports |
| `go test -tags fts5 -count=1 ./...` | Exit 0; 40 test-bearing packages passed |
| `go vet -tags fts5 ./...` | Exit 0 |
| `go build -tags fts5 ./...` | Exit 0 |
| `git diff --check` | Exit 0, no whitespace errors |
| Git scope checks | Only the three authorized files changed; index empty |

New-file diff inspections used `git diff --no-index /dev/null <file>`; exit 1
means the new file differs from `/dev/null`, not a failed check. HEAD remains
`0fc00cfb45cf29634515ab9e2fdad741cd55437d` at the recorded verification checkpoint.
The owner subsequently accepted M1 and authorized the local commit. Its hash is
reported after creation; it cannot be embedded in its own committed contents.
No push or merge is authorized.

### Limitations

- This is a complete snapshot of the approved core tables and three relevant
  flags, not a full database backup or migration authorization.
- LED fingerprints bind legacy API-level command intent, not actual firmware,
  segment transforms, physical wiring or device availability. No hardware was
  contacted. Legacy drawer/global-off behavior is not a new Locate workflow.
- Unknown/unresolved coordinates and unsafe mappings are captured as findings,
  not silently repaired; a complete snapshot with warnings is not proof that a
  migration or hardware action is safe.
- The mapping fingerprint includes complete row metadata and controller
  observations. Those can change independently of the target fingerprint.
- `ExecTx` provides a consistent snapshot, not a latest-state guarantee.
- The no-write guarantee covers application data, schema and audit mutations,
  not an absence of SQLite's own WAL/shared-memory filesystem housekeeping.
- `gopls` is not installed; no installation was authorized or performed.
  Compiler, targeted/full tests and vet provided the Go verification.

Approved commit: `feat: add read-only compatibility baseline`.

## Proposed M2 scope - additive storage foundation

Planning only; no M2 implementation or migration execution is authorized.

- Add cabinet records with exclusive controller ownership, stable drawer records
  with editable labels/order/geometry, and a separate drawer-to-existing-bin
  association table. Keep existing bin IDs and `part_assignments` unchanged.
- Add multiple drawer LED-range child records with explicit addressing semantics;
  keep electrical assignments separate from layout geometry, mounting and wiring
  paths. Do not equate physical strings with WLED segments or repurpose legacy
  containers as drawers. Final address semantics are an architectural prerequisite
  to approving M2 DDL; mounting/path editors are outside this milestone.
- Use additive Goose migrations and sqlc models/queries only. New tables start
  empty: no inferred cabinet/drawer creation, automatic legacy conversion, count
  reconciliation, or default 69-drawer layout. Existing runtime paths stay active;
  API/UI/Locate cutover is outside M2.
- Preserve all existing controller/container/bin/part/assignment rows, IDs, flags,
  quantities and mappings. Use foreign keys and restrictive deletion for new
  relationships; no inventory cascades. Rollback must leave legacy data unchanged
  and refuse to discard populated new tables without a separate explicit decision.
- Required tests: empty and legacy-populated fixture migrations; all three M1
  fingerprints unchanged before/after migration; upgrade/rollback preservation;
  ownership, foreign-key and safe-deletion constraints; multiple drawers/ranges,
  geometry/electrical independence and range validation; generated-code
  consistency, full tests, vet and build. Fixtures only, no production or hardware.

Next boundary: owner authorization of M2 scope and its permitted files. No
additional M1 audit cycle, push, merge, deployment or hardware action is requested.
