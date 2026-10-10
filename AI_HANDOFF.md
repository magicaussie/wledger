# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 18
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 017 — Read-Only LED Coordinate Conversion Preflight
Production-Authorization: READ_ONLY_VERIFICATION_ONLY
Current-Production-Commit: ca2789f54382bc2aa98b2d4535b09f9df1c21d55
Existing-Backup: /home/spetchal/backups/wledger-pre016-20261010T043616Z

## User authorization
The user approved PREPARING a read-only preflight for the possible segment-relative to drawer-relative LED coordinate conversion. This does NOT authorize executing the conversion, changing source, database, configuration, controller state, or sending LED commands.

## Objective
Establish, from actual deployed source and current production DB, exactly what the existing explicit conversion preview would do, whether it is safe, and which records would change. Report a go/no-go recommendation for a later separately authorized conversion.

## Instructions
1. Read AGENTS.md and verify Git/main, deployed release SHA, containers, backup existence/integrity, production DB goose version 10, integrity_check, foreign_key_check, current coordinate-space flag, drawer allocations, bin mappings, and counts. No writes or service restarts.
2. Inspect the exact source of the existing admin conversion preview and conversion executor (including authorization/CSRF/fingerprint and transactional validation), schema/query definitions and hardware LED addressing path. Determine whether the preview GET is genuinely read-only, including any implicit side effects. Do not call a route if its read-only behavior cannot be proven.
3. Use a safe read-only approach (SQLite read-only connection or verified read-only CLI, or source-based offline computation) to calculate the conversion preview for each drawer, including old and proposed LED indices, allocation start/count, segment/controller, affected bins, min/max and out-of-range/collision checks. If an exact row-level mapping cannot be obtained safely, report the limitation instead of guessing. No mutations, even to a test copy unless independently necessary and explicitly confined outside production.
4. Determine whether the proposed conversion preserves intended physical LED addresses given WLED segment offsets, and whether the drawer-relative coordinates and allocations are semantically correct. Distinguish verified facts from assumptions about hardware configuration. DO NOT test on physical hardware.
5. Identify blockers, ambiguous coordinates, unmapped bins, overlap, negative or out-of-bounds indices, preview fingerprint staleness, or conditions that should prevent execution. Check whether any system integrations (e.g., Home Assistant) would need later adjustment, but do not change them.
6. Report concise evidence: deployed commit, DB goose/flags, allocations, total affected rows, representative before→after mapping per drawer, collision/range checks, verified invariants, open risks, and explicit recommendation. Do not disclose secrets, credentials, or production DB contents unnecessarily.
7. Publish findings by replacing AI_HANDOFF.md with Sequence 19, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW, Task 017. Commit/push ONLY this handoff file to experiment/ai-handoff without force; no source commits, merges, or deployments. STOP.

## Strict boundaries
READ ONLY. No conversion POST, no admin mutation endpoints, no migration, no writes to production or production backup, no hardware/WLED commands, no controller changes, no service restarts, no changes to Home Assistant, and no next task automatically. Any subsequent conversion requires separate explicit user approval.
