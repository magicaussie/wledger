# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 32
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 023B — CSRF Protection for Hardware LED Actions
Production-Authorization: NO_PRODUCTION_CHANGES
Reviewed-Task-Commit: b7a7055007422f40d6e8f869a1083ec3535d8430
Task-Branch: fix/hardware-auth-and-secure-cookie

## Independent review
Reviewed actual committed source: internal/router/router.go, internal/router/router_auth_test.go, internal/config/security.go, internal/config/security_test.go, cmd/server/main.go, docs/environment.md. Task 023 authorization and Secure cookie changes approved as development checkpoint. Do not merge or deploy yet: the four physical LED POST endpoints remain without CSRF protection.

## Required follow-up
1. Inspect existing CSRF implementation, session handling and client-side requests for the four endpoints: POST /hardware/{id}/locate, /parts/{id}/locate, /drawers/{id}/locate, /hardware/off. Identify exact request mechanism (HTMX/fetch/forms) and how CSRF tokens are supplied elsewhere. Do not introduce a change that breaks the normal Locate/Off UI.
2. Implement CSRF protection for these four POST routes, preferably reusing existing middleware/patterns. Consider same-site browser request protections and the effect of SameSite=Lax; do not rely solely on SameSite as the explicit CSRF defense. Ensure tokens are present in actual UI requests and reject absent/invalid tokens.
3. Extend router-level regression tests: guest/viewer remain denied; editor/admin valid-CSRF requests succeed; missing/invalid CSRF rejected with zero WLED calls; no hardware contacted. Verify any non-browser clients have an appropriate authenticated path rather than weakening browser CSRF.
4. Recheck session cookie defaults and local HTTP opt-out for compatibility. No broad refactors, no unrelated formatting.
5. Run gofmt on changed Go files, build, vet, full go test -tags fts5 -count=1 ./..., targeted tests/race. Report source diffs, tests, risks.

## Review checkpoint
Continue on fix/hardware-auth-and-secure-cookie from b7a7055; commit and push a new review SHA (no force). Keep main unchanged. Publish Sequence 33 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 023B with branch, full SHA, parent, changed files, tests and any blockers. Only update AI_HANDOFF.md on experiment/ai-handoff. STOP.

## Boundaries
NO production deploy/restart, database writes/migrations, backup/restore, physical LED/WLED commands, Home Assistant changes or secrets. Stop if implementing correct CSRF requires a significant design decision.
