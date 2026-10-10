# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 76
From: ChatGPT
To: DeepSeek
Status: REQUESTED
Task: 044 — Read-only architecture and data audit for Cabinet → Drawer → Bin → Parts redesign
Production-Authorization: NONE
Reference-Roadmap: https://github.com/magicaussie/wledger/blob/main/ROADMAP.md
Main-Branch-After-Roadmap-Merge: 8922670c148dfde836a1fcc93b770fe6e00c6b29

## Goal

Investigate the existing WLEDger architecture, inventory data model, WLED mapping semantics, routes and UI. Deliver an evidence-based proposal for the new cabinet/drawer/bin hierarchy and phased migrations. **Do not implement migrations, change code, activate LEDs or deploy.**

## Guardrails

1. Read `ROADMAP.md` on `main`, `AI_HANDOFF.md`, repository instructions, and current source. Inspect Git status and branch/commit before proceeding.
2. Production mainserver = `spetchal@192.168.1.108`; workstation `spetchal-MS-7D75` hosts a different WLEDger Docker. Before any production read-only command, verify `hostname -s` and Docker host context explicitly. Do not conflate systems.
3. Production permission is **read-only inspection only**: no SQL writes, no database migrations, no Docker compose/restart/build, no file changes on mainserver, no inventory changes, no WLED API commands, no LED operations, no Home Assistant operations, no secrets in logs or handoff.
4. Prefer repository source and existing Task043 snapshot/metadata for baseline. If live DB inspection is needed, open SQLite `mode=ro`, do not alter the DB or create backup side effects. Do not claim read-only safety unless command is actually read-only.
5. Do not mutate `main`, `docs/storage-roadmap`, or production. This is a research task, not implementation authorization. You may document findings in a **new task branch** if appropriate; leave roadmap edits as proposed diffs until review.
6. Do not reinterpret existing 68 DB `bins` as physical drawers without evidence. User reports 69 physical units; investigate, do not silently correct the count.
7. Preserve current mapping fingerprint protocol and existing data. Never alter current WLED coordinate-space configuration during audit.

## Required audit

A. Enumerate relevant migrations/tables/columns/FKs/indexes for controllers, strings/segments, containers, bins, parts, assignments, quantities and any Wall/layout entities. Cite exact source file paths and relevant SQL definitions.

B. Trace read and write paths through Go services/repositories, HTTP routes, templ components, HTMX/JS, API/MCP where applicable. Identify what can be reused and what needs to change.

C. Explain current WLED mapping model with code evidence: controller vs physical string vs segment, bin LED coordinates, start/end inclusive/exclusive, any multi-range support, overlap constraints, and effects of locating an existing bin. Mark unknowns rather than assume.

D. Compare the last verified production counts (1 controller / 2 containers / 68 bins / 2 parts / 2 part assignments) with user's 69-physical-unit statement. Investigate read-only if available; provide plausible explanations separately from verified findings.

E. Propose minimal-risk target entities: cabinet with one controller and multiple strings; drawer geometry and multiple LED ranges across strings; bins with per-drawer geometry; multiple parts per bin and per-location quantities; existing part detail Locate button and drawer contents Grid/List default. Show FK relationships, migration sequencing, backwards compatibility and rollback strategy.

F. Provide a gap analysis against each Confirmed requirement in `ROADMAP.md`, with status: existing/reusable, partial, missing, or unknown.

G. Outline phase-by-phase implementation tasks, test plan and acceptance criteria. Distinguish **decisions requiring user input** from implementation decisions.

## Deliverable

Report findings with exact file paths and concrete evidence. Write next handoff as Sequence 77 on `experiment/ai-handoff` (or provide report in chat if that branch cannot be updated), including branch/commit references, verified findings, uncertainties, proposed schema, and recommended smallest first implementation slice. Do **not** start coding the redesign until ChatGPT/user review.
