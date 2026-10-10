# WLEDger OmO Development Handover

## Active session and authorization

This is the single active development handover document. `AGENTS.md` defines
development policy; the root `ROADMAP.md` records product requirements and design
updates. Historical handovers are references, not active instructions.

Current task: OmO documentation-only transition.

Status: documentation prepared for user review; verification evidence is
recorded below. No commit or publication is authorized.

The user approved edits only to `AGENTS.md` and `docs/OMO_HANDOVER.md`.
Application code, SQL schema, generated files, configuration and tests must
remain unchanged. Do not commit, push, deploy, access production, run database
migrations, activate WLED hardware or start Task 045.

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

These are locally inspected Git references. No fetch was performed and current
GitHub synchronization was not independently verified. No transition commit
exists.

## Responsibilities and delegation

GPT-6.1 Sol is the lead architect, orchestrator and independent reviewer, retaining
architecture, security and final acceptance responsibility.

DeepSeek V4.1 Flash is the automatic delegated implementation worker for bounded,
routine coding tasks. Sol delegates suitable approved work through OmO without
asking the user to approve each routine delegation.

Every delegation must specify scope, permitted files, acceptance criteria,
tests and prohibited operations. DeepSeek returns changed files, implementation
evidence, test results, failures, unrun checks and uncertainties. Sol independently
reviews actual source changes and verifies tests before acceptance.

No implementation worker was needed for this documentation-only transition.
Delegation never grants commit, push, deployment, production database or WLED
hardware authorization.

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

The read-only takeover assessment is complete. The user approved only the
documentation transition, not the cabinet redesign or any migration.

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

## Verification evidence

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

## Next proposed task

**Task 045 - Flexible Cabinet and LED Database Architecture**

Status: **design-only, awaiting approval; not started**.

Proposed scope: define the flexible cabinet/drawer/bin and LED architecture,
stable identity relationships, physical mounting and electrical addressing
separation, and evidence-based migration/compatibility strategy.

This entry records a proposal only. Do not begin its design, implementation,
schema changes, migrations or production inspection without explicit approval.
