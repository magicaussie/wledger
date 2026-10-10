# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 12
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 014 — Stage C Post-Upgrade Functional Verification
Production-Authorization: READ_ONLY_VERIFICATION_ONLY
Target-Commit: 63016f4e75134ae0675934818fb77f53dd463800
Backup: /home/spetchal/backups/wledger-20261010T040208Z

## Objective
Independently verify WLEDger production after Stage B, with emphasis on drawer/bin UI and LED configuration in the existing segment-relative coordinate space. This is a NON-DESTRUCTIVE test. Do not modify data or send physical LED commands.

## Scope
1. Read AGENTS.md and verify live containers/images, deployed release commit, production DB goose v10, flags, integrity, foreign keys, row counts, 68 mapped bins, and pre/post-upgrade LED mapping digest.
2. Inspect the relevant committed source for cabinet/drawer/bin UI handlers, templates, coordinate-space rendering, edit guards, and WLED locate mapping. Distinguish expected behavior from observed behavior.
3. Test read-only web navigation for login, cabinet, drawer, bin and inventory pages. Use an existing authorized test session only if one is already safely available; do not extract credentials, create users, change passwords, or bypass authentication. If authenticated pages cannot be reached safely, mark UI behavior NOT TESTED and rely on source review only.
4. Check any read-only API endpoints and health checks that can be safely accessed with existing authorization, without disclosing tokens. Do not invoke MCP tools that could mutate inventory or activate LEDs.
5. Verify that segment-relative bin indices and drawer allocations are presented consistently, and identify any confusing UI indications or paths that could accidentally trigger a coordinate conversion. No form submissions, POST/PUT/PATCH/DELETE requests or LED commands.
6. Inspect runtime logs for errors since upgrade, sanitize sensitive information, and check container restarts/uptime. Recheck backup assets remain intact if feasible.
7. Produce an evidence table: PASS, FAIL, NOT TESTED; exact URLs/routes or source paths checked, without exposing secrets. Highlight any unresolved risks and recommend the smallest safe next step.

## Strict restrictions
- No production database or filesystem writes (except ordinary incidental application access logs if unavoidable), no configuration edits, no migrations, no backup/restore, no restarts, no deployment, no credentials changes.
- No manual LED coordinate conversion, no WLED network requests, no LED locate commands, no hardware operations.
- Home Assistant and external MCP connectivity are explicitly out of scope and not blockers.
- If authenticated browser tests are unavailable, do NOT attempt to manufacture a session or bypass login.
- Do not begin any further task automatically.

## Handoff
When complete, fetch and verify the handoff branch tip for concurrency; replace this entire file with Sequence 13, From DeepSeek, To ChatGPT, Status AWAITING_REVIEW. Report precise checks, evidence, PASS/FAIL/NOT TESTED, observed UI behavior versus source-only conclusions, and any issues. Commit only AI_HANDOFF.md and push only experiment/ai-handoff without force. Do not touch main.
