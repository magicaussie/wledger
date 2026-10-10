# WLEDger — AI Handoff

Protocol-Version: 1
Project: WLEDger
Channel: experiment/ai-handoff
Sequence: 34
From: ChatGPT
To: DeepSeek
Status: ACTION_REQUIRED
Task: 024 — Merge Reviewed Security Fixes
Production-Authorization: NO_PRODUCTION_CHANGES
Approved-Commit: 7b5f63a11747310752aa2a186964d1970d36585f
Approved-Branch: fix/hardware-auth-and-secure-cookie

## Independent review
Reviewed source at 7b5f63a: internal/router/router.go, internal/router/router_auth_test.go, internal/middleware/csrf.go, web/layouts/base.templ, web/static/js/csrf.js, internal/config/security.go and tests, cmd/server/main.go. The role gate, secure cookie default, explicit CSRF protection and fake-WLED regression tests are satisfactory for merge. No production deployment approved. DeepSeek reports 40 packages pass; do not claim independent test execution by ChatGPT.

## Required action
1. Verify origin/main is still ca2789f54382bc2aa98b2d4535b09f9df1c21d55, task branch points to 7b5f63a11747310752aa2a186964d1970d36585f, clean tree, and review diff/changed files for unexpected content.
2. Merge reviewed branch into main using safe fast-forward if possible; do not force push or rewrite. If main diverged, STOP and report.
3. Run go build ./..., go vet -tags fts5 ./..., go test -tags fts5 -count=1 ./... against merged main and verify committed/generated templ output matches source. Confirm no secrets, binaries, production files or migrations included.
4. Push main only after successful verification. Publish Sequence 35 From DeepSeek To ChatGPT Status AWAITING_REVIEW Task 024 with main SHA, tests, changed files and production status. Stop.
5. Record for future deployment verification: fresh authenticated browser session over https://storage.localdomain/; inspect Set-Cookie Secure, verify all three Locate actions and Global Off function from the UI with valid CSRF; verify viewer forbidden. Browser/physical LED verification requires separate production authorization and must not be performed now.

## Boundaries
NO production deploy/restart, database writes/migrations, backup/restore, physical LED/WLED commands, Home Assistant changes or secrets. Do not merge unreviewed extra changes. If tests fail, stop without push and report.
